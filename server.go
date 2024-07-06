package xconn

import (
	"fmt"
	"log"
	"net"

	"github.com/xconnio/wampproto-go/auth"
)

type Server struct {
	router   *Router
	acceptor *WebSocketAcceptor
}

func NewServer(router *Router, authenticator auth.ServerAuthenticator) *Server {
	acceptor := &WebSocketAcceptor{
		Authenticator: authenticator,
	}

	return &Server{
		router:   router,
		acceptor: acceptor,
	}
}

func (s *Server) RegisterSpec(spec WSSerializerSpec) error {
	return s.acceptor.RegisterSpec(spec)
}

func (s *Server) Start(host string, port int) error {
	address := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	fmt.Printf("listening on ws://%s/ws\n", address)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
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

			for {
				msg, err := base.ReadMessage()
				if err != nil {
					_ = s.router.DetachClient(base)
					break
				}

				if err = s.router.ReceiveMessage(base, msg); err != nil {
					log.Println(err)
					return
				}
			}
		}()
	}
}
