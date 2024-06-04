package xconn

import (
	"fmt"
	"log"
	"net"

	"github.com/xconnio/wampproto-go/auth"
)

type Server struct {
	router        *Router
	authenticator auth.ServerAuthenticator
}

func NewServer(router *Router, authenticator auth.ServerAuthenticator) *Server {
	return &Server{
		router:        router,
		authenticator: authenticator,
	}
}

func (s *Server) Start(host string, port int) error {
	address := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}

		go func() {
			acceptor := &WebSocketAcceptor{}
			base, err := acceptor.Accept(conn)
			if err != nil {
				log.Println(err)
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
