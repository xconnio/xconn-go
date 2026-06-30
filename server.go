package xconn

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/projectdiscovery/ratelimit"
	"github.com/quic-go/quic-go"
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

// ListenAndServeQUIC starts a QUIC listener. Each QUIC connection is multiplexed:
// every incoming stream that begins with the RawSocket magic byte establishes an
// independent WAMP session; other streams are delivered as raw data streams.
func (s *Server) ListenAndServeQUIC(address string, tlsConfig *tls.Config) (*QUICListener, error) {
	if tlsConfig == nil {
		return nil, fmt.Errorf("tls config is required for QUIC")
	}

	if !slices.Contains(tlsConfig.NextProtos, NextProtoWAMP) {
		tlsConfig = tlsConfig.Clone()
		tlsConfig.NextProtos = append(tlsConfig.NextProtos, NextProtoWAMP)
	}

	ln, err := quic.ListenAddr(address, tlsConfig, DefaultQuicConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	l := &QUICListener{
		Listener: &Listener{closer: ln, addr: ln.Addr()},
		server:   s,
		conns:    make(chan *QUICConn, 8),
		streams:  make(chan *QUICStream, 8),
		done:     make(chan struct{}),
	}
	go l.startConnectionLoop(ln)
	return l, nil
}

// QUICListener is returned by ListenAndServeQUIC.
type QUICListener struct {
	*Listener
	server  *Server
	conns   chan *QUICConn
	streams chan *QUICStream
	done    chan struct{}
	sync.Once
	sync.WaitGroup
}

// Close closes the underlying QUIC listener and signals all internal goroutines to stop.
func (l *QUICListener) Close() error {
	err := l.Listener.Close()
	l.Do(func() { close(l.done) })
	return err
}

// Conns returns a channel that receives an event each time an authenticated QUIC client connects.
func (l *QUICListener) Conns() <-chan *QUICConn {
	return l.conns
}

// AcceptStream receives one event per raw stream opened by a client.
func (l *QUICListener) AcceptStream() <-chan *QUICStream {
	return l.streams
}

func (l *QUICListener) startConnectionLoop(ln *quic.Listener) {
	defer func() {
		// Ensure done is closed even if the loop exits without an explicit Close().
		l.Do(func() { close(l.done) })
		// Wait for all handler goroutines before closing channels so no sender
		// races with the close.
		l.Wait()
		close(l.conns)
		close(l.streams)
	}()
	for {
		conn, err := ln.Accept(context.Background())
		if err != nil {
			return
		}
		l.Add(1)
		go l.handleQUICConnection(conn)
	}
}

// handleQUICConnection accepts every incoming stream on conn and dispatches each one independently.
// WAMP streams (magic byte 0x7F) become new WAMP sessions.
// Any other stream is delivered on the raw AcceptStream channel.
func (l *QUICListener) handleQUICConnection(conn *quic.Conn) {
	defer l.Done()

	var streamWg sync.WaitGroup
	defer streamWg.Wait()

	for {
		stream, err := conn.AcceptStream(context.Background())
		if err != nil {
			return
		}
		streamWg.Add(1)
		go func(s *quic.Stream) {
			defer streamWg.Done()
			l.dispatchQUICStream(conn, s)
		}(stream)
	}
}

// dispatchQUICStream routes a stream: WAMP RawSocket magic (0x7F) → WAMP session, anything else → raw stream.
func (l *QUICListener) dispatchQUICStream(conn *quic.Conn, stream *quic.Stream) {
	streamConn := newQUICStreamConn(conn, stream)

	// bufio.NewReader fills its internal buffer on the first read, so a single conn.Read call
	// inside UpgradeRawSocket gets the full 4-byte handshake without us consuming it.
	br := bufio.NewReader(streamConn)
	magic, err := br.Peek(1)

	// Wrap br so downstream code sees buffered data + raw stream as one net.Conn.
	wrapped := connWithPrependedReader{
		Reader: br,
		Conn:   streamConn,
	}

	if err == nil && magic[0] == transports.MAGIC {
		l.handleWAMPStream(conn, wrapped)
	} else {
		select {
		case l.streams <- &QUICStream{Conn: wrapped}:
		case <-l.done:
		}
	}
}

// handleWAMPStream runs a full WAMP session on a single stream: RawSocket handshake, router attach, message loop.
func (l *QUICListener) handleWAMPStream(conn *quic.Conn, streamConn net.Conn) {
	s := l.server

	config := DefaultRawSocketServerConfig()
	config.KeepAliveInterval = s.keepAliveInterval
	config.KeepAliveTimeout = s.keepAliveTimeout
	config.OutQueueSize = s.outQueueSize

	base, err := s.rsAcceptor.Accept(streamConn, config)
	if err != nil {
		log.Debugf("failed to accept quic WAMP stream: %v", err)
		return
	}

	if err = s.router.AttachClient(base); err != nil {
		log.Debugf("failed to attach quic client: %v", err)
		return
	}

	// sessCtx is per-session: cancelled when THIS session's loop exits, not
	// when the whole QUIC connection closes.
	sessCtx, sessCancel := context.WithCancel(context.Background())

	// Deliver the event asynchronously so the WAMP message loop can start
	// immediately (mirroring the original single-session design). The goroutine
	// blocks until the consumer drains Conns() or the listener shuts down.
	go func() {
		select {
		case l.conns <- &QUICConn{Ctx: sessCtx, Session: base, Conn: &QUICClientConn{conn: conn}}:
		case <-l.done:
		}
	}()

	log.Debugf("attached quic client %d", base.ID())

	var limiter *ratelimit.Limiter
	if s.throttle != nil {
		limiter = s.throttle.Create()
	}

	for {
		msg, err := base.ReadMessage()
		if err != nil {
			log.Debugf("failed to read quic client message: %v", err)
			_ = s.router.DetachClient(base)
			break
		}

		if limiter != nil {
			limiter.Take()
		}

		if err = s.router.ReceiveMessage(base, msg); err != nil {
			log.Debugf("error feeding quic client message to router: %v", err)
		}
	}

	sessCancel()
	log.Debugf("detached quic client %d", base.ID())
}
