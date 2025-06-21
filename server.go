package xconn

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/projectdiscovery/ratelimit"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/xconn-go/internal"
)

type Listener int

const (
	ListenerWebSocket Listener = iota
	ListenerRawSocket
	ListenerUnixSocket
	ListenerUniversalTCP
)

type Server struct {
	router            *Router
	wsAcceptor        *WebSocketAcceptor
	rsAcceptor        *RawSocketAcceptor
	throttle          *internal.Throttle
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

func (s *Server) RegisterSpec(spec WSSerializerSpec) error {
	return s.wsAcceptor.RegisterSpec(spec)
}

func (s *Server) StartWebSocket(host string, port int) (io.Closer, error) {
	address := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port
	fmt.Printf("listening on ws://%s:%d/ws\n", host, actualPort)

	go s.startConnectionLoop(ln, ListenerWebSocket)

	return ln, err
}

func (s *Server) HandleClient(conn net.Conn, listener Listener) {
	var base BaseSession
	var err error

	switch listener {
	case ListenerUniversalTCP:
		// read first byte from conn and check if its rawsocket magic, otherwise do websocket.
		return
	case ListenerWebSocket:
		config := DefaultWebSocketServerConfig()
		config.KeepAliveInterval = s.keepAliveInterval
		config.KeepAliveTimeout = s.keepAliveTimeout
		base, err = s.wsAcceptor.Accept(conn, config)
		if err != nil {
			return
		}
	case ListenerRawSocket, ListenerUnixSocket:
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

func (s *Server) StartUnixSocket(udsPath string) (io.Closer, error) {
	if err := os.RemoveAll(udsPath); err != nil {
		return nil, fmt.Errorf("failed to remove old UDS file: %w", err)
	}

	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on UDS: %w", err)
	}

	fmt.Printf("listening on unix://%s\n", udsPath)

	go s.startConnectionLoop(ln, ListenerUnixSocket)

	return ln, err
}

func (s *Server) StartRawSocket(host string, port int) (io.Closer, error) {
	address := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port
	fmt.Printf("listening on rs://%s:%d/", host, actualPort)

	go s.startConnectionLoop(ln, ListenerRawSocket)

	return ln, err
}

func (s *Server) StartUniversalTCP(host string, port int) (io.Closer, error) {
	address := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port
	fmt.Printf("listening on rs://%s:%d/", host, actualPort)
	fmt.Printf("listening on ws://%s:%d/ws", host, actualPort)

	go s.startConnectionLoop(ln, ListenerUniversalTCP)

	return ln, err
}
