package main

import (
	"fmt"
	"os"
	"os/signal"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/xconn-go"
)

const (
	realm               = "realm1"
	authRole            = "trusted"
	ticket              = "ticket-pass"
	craAuthID           = "cra-user"
	craSecret           = "secret123"
	cryptosignPublicKey = "21e04c9f0a54b3c6c045a3a1d1966cb4bf3e79ecc555ba0267222e624b502e6a"
)

type Authenticator struct{}

func NewAuthenticator() *Authenticator {
	return &Authenticator{}
}

func (a *Authenticator) Methods() []auth.Method {
	return []auth.Method{auth.MethodAnonymous, auth.MethodTicket, auth.MethodCRA, auth.MethodCryptoSign}
}

func (a *Authenticator) Authenticate(request auth.Request) (auth.Response, error) {
	switch request.AuthMethod() {
	case auth.MethodAnonymous:
		if request.Realm() == realm {
			return auth.NewResponse(request.AuthID(), authRole, 0)
		}
		return nil, fmt.Errorf("invalid realm")

	case auth.MethodTicket:
		ticketRequest, ok := request.(*auth.TicketRequest)
		if !ok {
			return nil, fmt.Errorf("invalid request")
		}
		if ticketRequest.Ticket() == ticket {
			return auth.NewResponse(ticketRequest.AuthID(), authRole, 0)
		}
		return nil, fmt.Errorf("invalid ticket")

	case auth.MethodCRA:
		if request.AuthID() == craAuthID {
			return auth.NewCRAResponse(craAuthID, authRole, craSecret, 0), nil
		}
		return nil, fmt.Errorf("invalid CRA user")

	case auth.MethodCryptoSign:
		cryptosignRequest, ok := request.(*auth.RequestCryptoSign)
		if !ok {
			return nil, fmt.Errorf("invalid request")
		}
		if cryptosignPublicKey == cryptosignRequest.PublicKey() {
			return auth.NewResponse(cryptosignRequest.AuthID(), authRole, 0)
		}
		return nil, fmt.Errorf("unknown publickey")

	default:
		return nil, fmt.Errorf("unknown authentication method: %v", request.AuthMethod())
	}
}

func main() {
	r := xconn.NewRouter()
	if err := r.AddRealm(realm); err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	authenticator := NewAuthenticator()
	server := xconn.NewServer(r, authenticator, nil)
	closer, err := server.ListenAndServeWebSocket(xconn.NetworkTCP, "localhost:8080")
	if err != nil {
		log.Fatal("Failed to start server:", err)
	}
	defer closer.Close()
	log.Println("Listening on ws://localhost:8080/")

	// Close server if SIGINT (CTRL-c) received.
	closeChan := make(chan os.Signal, 1)
	signal.Notify(closeChan, os.Interrupt)
	<-closeChan
}
