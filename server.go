package xconn

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"time"

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
	router                 *Router
	wsAcceptor             *WebSocketAcceptor
	rsAcceptor             *RawSocketAcceptor
	throttle               *Throttle
	keepAliveInterval      time.Duration
	keepAliveTimeout       time.Duration
	outQueueSize           int
	noOfWSListeners        int
	noOfRSListeners        int
	noOfUniversalListeners int
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
		server.noOfWSListeners = config.NoOfWSListeners
		server.noOfRSListeners = config.NoOfRSListeners
		server.noOfUniversalListeners = config.NoOfUniversalListeners
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

	return s.Serve(ln, ListenerWebSocket, s.noOfWSListeners), nil
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

	return s.Serve(ln, ListenerRawSocket, s.noOfRSListeners), nil
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

	return s.Serve(ln, ListenerUniversalTCP, s.noOfUniversalListeners), nil
}

func (s *Server) Serve(listener net.Listener, protocol ListenerType, noOfListeners int) *Listener {
	if noOfListeners == 0 {
		noOfListeners = 1
	}
	for i := 0; i < noOfListeners; i++ {
		go s.startConnectionLoop(listener, protocol)
	}

	addr := listener.Addr()
	return &Listener{
		closer: listener,
		addr:   addr,
	}
}
