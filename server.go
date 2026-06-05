package xconn

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/projectdiscovery/ratelimit"
	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/transports"
)

type ListenerType int

const (
	ListenerWebSocket ListenerType = iota
	ListenerRawSocket
	ListenerUniversalTCP

	ServerOutQueueSizeDefault = 8
)

type Network string

const (
	NetworkUnix Network = "unix"
	NetworkTCP  Network = "tcp"
)

type Server struct {
	router            *Router
	wsAcceptor        *WebSocketAcceptor
	rsAcceptor        *RawSocketAcceptor
	throttle          *Throttle
	keepAliveInterval time.Duration
	keepAliveTimeout  time.Duration
	outQueueSize      int
	yamuxConns        chan *YamuxConn
	yamuxStreams      chan *YamuxStream
}

func NewServer(router *Router, authenticator auth.ServerAuthenticator, config *ServerConfig) *Server {
	wsAcceptor := &WebSocketAcceptor{
		Authenticator: authenticator,
	}

	rsAcceptor := &RawSocketAcceptor{
		Authenticator: authenticator,
	}

	server := &Server{
		router:     router,
		wsAcceptor: wsAcceptor,
		rsAcceptor: rsAcceptor,
	}

	if config != nil {
		server.throttle = config.Throttle
		server.keepAliveInterval = config.KeepAliveInterval
		server.keepAliveTimeout = config.KeepAliveTimeout
		server.outQueueSize = config.OutQueueSize
	}

	return server
}

func (s *Server) RegisterSpec(spec SerializerSpec) error {
	if err := s.wsAcceptor.RegisterSpec(spec); err != nil {
		return err
	}

	return s.rsAcceptor.RegisterSpec(spec)
}

type Listener struct {
	closer io.Closer
	addr   net.Addr
}

func NewListener(closer io.Closer, addr net.Addr) *Listener {
	return &Listener{
		closer: closer,
		addr:   addr,
	}
}

func (l *Listener) Close() error {
	return l.closer.Close()
}

func (l *Listener) Addr() net.Addr {
	return l.addr
}

func (l *Listener) Port() int {
	if tcp, ok := l.addr.(*net.TCPAddr); ok {
		return tcp.Port
	}

	return 0
}

func ensureUnixSocketAvailable(udsPath string) error {
	conn, err := net.DialTimeout(string(NetworkUnix), udsPath, time.Second)
	if err == nil {
		_ = conn.Close()
		return fmt.Errorf("socket at %s is already in use", udsPath)
	}

	fileInfo, err := os.Lstat(udsPath)
	if err == nil {
		if fileInfo.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("file at %s exists and is not a unix socket", udsPath)
		}

		if err := os.Remove(udsPath); err != nil {
			return fmt.Errorf("failed to remove old unix socket file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error checking unix socket path: %w", err)
	}

	return nil
}

func (s *Server) ListenAndServeWebSocket(network Network, address string) (*Listener, error) {
	if network == NetworkUnix {
		if err := ensureUnixSocketAvailable(address); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen(string(network), address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	return s.Serve(ln, ListenerWebSocket), nil
}

type connWithPrependedReader struct {
	io.Reader // to override Read()
	net.Conn  // for all other methods
}

func (c connWithPrependedReader) Read(p []byte) (int, error) {
	return c.Reader.Read(p)
}

func (s *Server) HandleClient(conn net.Conn, listener ListenerType) {
	var base BaseSession
	var err error

	switch listener {
	case ListenerUniversalTCP:
		reader := bufio.NewReader(conn)
		magicArray, err := reader.Peek(1)
		if err != nil {
			log.Printf("failed to peek header from client: %v", err)
			_ = conn.Close()
			return
		}

		wrapped := connWithPrependedReader{
			Reader: io.MultiReader(reader, conn),
			Conn:   conn,
		}

		if magicArray[0] == transports.MAGIC {
			config := DefaultRawSocketServerConfig()
			config.KeepAliveInterval = s.keepAliveInterval
			config.KeepAliveTimeout = s.keepAliveTimeout
			config.OutQueueSize = s.outQueueSize
			base, err = s.rsAcceptor.Accept(wrapped, config)
			if err != nil {
				return
			}
		} else {
			config := DefaultWebSocketServerConfig()
			config.KeepAliveInterval = s.keepAliveInterval
			config.KeepAliveTimeout = s.keepAliveTimeout
			config.OutQueueSize = s.outQueueSize
			base, err = s.wsAcceptor.Accept(wrapped, s.router, config)
			if err != nil {
				return
			}
		}
	case ListenerWebSocket:
		config := DefaultWebSocketServerConfig()
		config.KeepAliveInterval = s.keepAliveInterval
		config.KeepAliveTimeout = s.keepAliveTimeout
		config.OutQueueSize = s.outQueueSize
		base, err = s.wsAcceptor.Accept(conn, s.router, config)
		if err != nil {
			log.Debugf("failed to accept websocket connection: %v", err)
			return
		}
	case ListenerRawSocket:
		config := DefaultRawSocketServerConfig()
		config.KeepAliveInterval = s.keepAliveInterval
		config.KeepAliveTimeout = s.keepAliveTimeout
		config.OutQueueSize = s.outQueueSize
		base, err = s.rsAcceptor.Accept(conn, config)
		if err != nil {
			log.Debugf("failed to accept rawsocket connection: %v", err)
			return
		}
	default:
		return
	}

	if err = s.router.AttachClient(base); err != nil {
		log.Debugf("failed to attach client: %v", err)
		return
	}

	log.Debugf("attached client %d", base.ID())

	var limiter *ratelimit.Limiter
	if s.throttle != nil {
		limiter = s.throttle.Create()
	}

	for {
		msg, err := base.ReadMessage()
		if err != nil {
			log.Tracef("failed to read client message: %v", err)
			_ = s.router.DetachClient(base)
			break
		}

		if limiter != nil {
			limiter.Take()
		}

		if err = s.router.ReceiveMessage(base, msg); err != nil {
			// This likely means we were not able to write the message
			// to the other peer, they are either blocked, or maybe just
			// disconnected. In either case, we just log that and continue
			// reading further messages.
			log.Tracef("error feeding client message to router: %v", err)
		}
	}

	log.Debugf("detached client %d", base.ID())
}

func (s *Server) startConnectionLoop(ln net.Listener, listener ListenerType) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			_ = ln.Close()
			return
		}

		go s.HandleClient(conn, listener)
	}
}

func (s *Server) ListenAndServeRawSocket(network Network, address string) (*Listener, error) {
	if network == NetworkUnix {
		if err := ensureUnixSocketAvailable(address); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen(string(network), address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	return s.Serve(ln, ListenerRawSocket), nil
}

func (s *Server) ListenAndServeUniversal(network Network, address string) (*Listener, error) {
	if network == NetworkUnix {
		if err := ensureUnixSocketAvailable(address); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen(string(network), address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	return s.Serve(ln, ListenerUniversalTCP), nil
}

func (s *Server) Serve(listener net.Listener, protocol ListenerType) *Listener {
	go s.startConnectionLoop(listener, protocol)

	addr := listener.Addr()
	return &Listener{
		closer: listener,
		addr:   addr,
	}
}

// YamuxListener is returned by ListenAndServeYamux.
type YamuxListener struct {
	*Listener
	conns   <-chan *YamuxConn
	streams <-chan *YamuxStream
}

// Conns receives one event per authenticated yamux client connection.
func (l *YamuxListener) Conns() <-chan *YamuxConn {
	return l.conns
}

// AcceptStream receives one event per raw stream opened by a client.
func (l *YamuxListener) AcceptStream() <-chan *YamuxStream {
	return l.streams
}

// ListenAndServeYamux starts a yamux listener. Each TCP connection is multiplexed.
// The first stream carries WAMP, establishing an authenticated session.
func (s *Server) ListenAndServeYamux(network Network, address string) (*YamuxListener, error) {
	if network == NetworkUnix {
		if err := ensureUnixSocketAvailable(address); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen(string(network), address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	conns := make(chan *YamuxConn, 8)
	streams := make(chan *YamuxStream, 8)
	s.yamuxConns = conns
	s.yamuxStreams = streams

	go s.startYamuxConnectionLoop(ln)
	return &YamuxListener{
		Listener: &Listener{closer: ln, addr: ln.Addr()},
		conns:    conns,
		streams:  streams,
	}, nil
}

func (s *Server) startYamuxConnectionLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			_ = ln.Close()
			return
		}
		go s.HandleYamuxClient(conn)
	}
}

// HandleYamuxClient handles a single TCP connection as a yamux session.
// The first accepted stream carries the WAMP protocol.
func (s *Server) HandleYamuxClient(conn net.Conn) {
	yamuxSess, err := yamux.Server(conn, yamuxConfig())
	if err != nil {
		log.Debugf("failed to create yamux session: %v", err)
		_ = conn.Close()
		return
	}

	wampStream, err := yamuxSess.Accept()
	if err != nil {
		log.Debugf("failed to accept WAMP stream: %v", err)
		_ = yamuxSess.Close()
		return
	}

	config := DefaultRawSocketServerConfig()
	config.KeepAliveInterval = s.keepAliveInterval
	config.KeepAliveTimeout = s.keepAliveTimeout
	config.OutQueueSize = s.outQueueSize

	base, err := s.rsAcceptor.Accept(wampStream, config)
	if err != nil {
		log.Debugf("failed to accept yamux WAMP stream: %v", err)
		_ = yamuxSess.Close()
		return
	}

	if err = s.router.AttachClient(base); err != nil {
		log.Debugf("failed to attach yamux client: %v", err)
		_ = yamuxSess.Close()
		return
	}

	// ctx is cancelled when this connection closes so Conns consumers can clean up.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if s.yamuxConns != nil {
		s.yamuxConns <- &YamuxConn{Ctx: ctx, Session: base, Conn: &YamuxClientConn{yamuxSess: yamuxSess}}
	}

	if s.yamuxStreams != nil {
		go s.acceptYamuxStreams(yamuxSess, base)
	}

	log.Debugf("attached yamux client %d", base.ID())

	var limiter *ratelimit.Limiter
	if s.throttle != nil {
		limiter = s.throttle.Create()
	}

	for {
		msg, err := base.ReadMessage()
		if err != nil {
			log.Debugf("failed to read yamux client message: %v", err)
			_ = s.router.DetachClient(base)
			break
		}

		if limiter != nil {
			limiter.Take()
		}

		if err = s.router.ReceiveMessage(base, msg); err != nil {
			log.Debugf("error feeding yamux client message to router: %v", err)
		}
	}

	_ = yamuxSess.Close()
	log.Debugf("detached yamux client %d", base.ID())
}

func (s *Server) acceptYamuxStreams(yamuxSess *yamux.Session, base BaseSession) {
	for {
		stream, err := yamuxSess.Accept()
		if err != nil {
			return
		}
		s.yamuxStreams <- &YamuxStream{Session: base, Conn: stream}
	}
}
