package xconn

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/projectdiscovery/ratelimit"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/transports"
)

type Listener int

const (
	ListenerWebSocket Listener = iota
	ListenerRawSocket
	ListenerUnixSocket
	ListenerUniversalTCP
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
	}

	return server
}

func (s *Server) RegisterSpec(spec SerializerSpec) error {
	if err := s.wsAcceptor.RegisterSpec(spec); err != nil {
		return err
	}

	return s.rsAcceptor.RegisterSpec(spec)
}

func (s *Server) ListenAndServeWebSocket(network Network, address string) (io.Closer, error) {
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

func (s *Server) HandleClient(conn net.Conn, listener Listener) {
	var base BaseSession
	var err error

	switch listener {
	case ListenerUniversalTCP:
		reader := bufio.NewReader(conn)
		magicArray, err := reader.Peek(1)
		if err != nil {
			fmt.Printf("failed to peek header from client: %v\n", err)
			return
		}

		wrapped := connWithPrependedReader{
			Reader: io.MultiReader(reader, conn),
			Conn:   conn,
		}

		if magicArray[0] == transports.MAGIC {
			base, err = s.rsAcceptor.Accept(wrapped)
			if err != nil {
				return
			}
		} else {
			config := DefaultWebSocketServerConfig()
			config.KeepAliveInterval = s.keepAliveInterval
			config.KeepAliveTimeout = s.keepAliveTimeout
			base, err = s.wsAcceptor.Accept(wrapped, s.router, config)
			if err != nil {
				return
			}
		}
	case ListenerWebSocket:
		config := DefaultWebSocketServerConfig()
		config.KeepAliveInterval = s.keepAliveInterval
		config.KeepAliveTimeout = s.keepAliveTimeout
		base, err = s.wsAcceptor.Accept(conn, s.router, config)
		if err != nil {
			return
		}
	case ListenerRawSocket:
		base, err = s.rsAcceptor.Accept(conn)
		if err != nil {
			return
		}
	default:
		return
	}

	if err = s.router.AttachClient(base); err != nil {
		log.Println(err)
		return
	}

	var limiter *ratelimit.Limiter
	if s.throttle != nil {
		limiter = s.throttle.Create()
	}

	for {
		msg, err := base.ReadMessage()
		if err != nil {
			_ = s.router.DetachClient(base)
			break
		}

		if limiter != nil {
			limiter.Take()
		}

		if err = s.router.ReceiveMessage(base, msg); err != nil {
			log.Println(err)
			return
		}
	}
}

func (s *Server) startConnectionLoop(ln net.Listener, listener Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			_ = ln.Close()
			return
		}

		go s.HandleClient(conn, listener)
	}
}

func (s *Server) ListenAndServeRawSocket(network Network, address string) (io.Closer, error) {
	ln, err := net.Listen(string(network), address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	return s.Serve(ln, ListenerRawSocket), nil
}

func (s *Server) ListenAndServeUniversal(network Network, address string) (io.Closer, error) {
	ln, err := net.Listen(string(network), address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	return s.Serve(ln, ListenerUniversalTCP), nil
}

func (s *Server) Serve(listener net.Listener, protocol Listener) io.Closer {
	go s.startConnectionLoop(listener, protocol)
	return listener
}
