package xconn

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"

	"github.com/projectdiscovery/ratelimit"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/xconn-go/internal"
)

type Server struct {
	router   *Router
	acceptor *WebSocketAcceptor
	throttle *internal.Throttle
}

func NewServer(router *Router, authenticator auth.ServerAuthenticator, throttle *internal.Throttle) *Server {
	acceptor := &WebSocketAcceptor{
		Authenticator: authenticator,
	}

	return &Server{
		router:   router,
		acceptor: acceptor,
		throttle: throttle,
	}
}

func (s *Server) RegisterSpec(spec WSSerializerSpec) error {
	return s.acceptor.RegisterSpec(spec)
}

func (s *Server) Start(host string, port int) (io.Closer, error) {
	address := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port
	fmt.Printf("listening on ws://%s:%d/ws\n", host, actualPort)

	go s.startConnectionLoop(ln)

	return ln, err
}

func (s *Server) startConnectionLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			_ = ln.Close()
			return
		}

		go func() {
			base, err := s.acceptor.Accept(conn)
			if err != nil {
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
		}()
	}
}

func (s *Server) StartUnixServer(udsPath string) (io.Closer, error) {
	if err := os.RemoveAll(udsPath); err != nil {
		return nil, fmt.Errorf("failed to remove old UDS file: %w", err)
	}

	ln, err := net.Listen("unix", udsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on UDS: %w", err)
	}

	fmt.Printf("listening on unix://%s\n", udsPath)

	go s.startConnectionLoop(ln)

	return ln, err
}
