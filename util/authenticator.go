package util

import (
	"fmt"

	"golang.org/x/exp/slices"

	"github.com/xconnio/wampproto-go/auth"
)

type Authenticator struct {
	authenticator Authenticators
}

func NewAuthenticator(authenticators Authenticators) *Authenticator {
	return &Authenticator{authenticator: authenticators}
}

func (a *Authenticator) Methods() []auth.Method {
	return []auth.Method{auth.MethodAnonymous, auth.MethodTicket, auth.MethodCRA, auth.MethodCryptoSign}
}

func (a *Authenticator) Authenticate(request auth.Request) (auth.Response, error) {
	switch request.AuthMethod() {
	case auth.MethodAnonymous:
		for _, anonymous := range a.authenticator.Anonymous {
			if anonymous.Realm == request.Realm() {
				return auth.NewResponse(request.AuthID(), request.AuthRole(), 0)
			}
		}
		return nil, fmt.Errorf("invalid realm")

	case auth.MethodTicket:
		ticketRequest, ok := request.(*auth.TicketRequest)
		if !ok {
			return nil, fmt.Errorf("invalid request")
		}

		for _, ticket := range a.authenticator.Ticket {
			if ticket.Realm == ticketRequest.Realm() && ticket.Ticket == ticketRequest.Ticket() {
				return auth.NewResponse(ticketRequest.AuthID(), ticketRequest.AuthRole(), 0)
			}
		}
		return nil, fmt.Errorf("invalid ticket")

	case auth.MethodCRA:
		for _, wampcra := range a.authenticator.WAMPCRA {
			if wampcra.Realm == request.Realm() && request.AuthID() == wampcra.AuthID {
				if wampcra.Salt != "" {
					return auth.NewCRAResponseSalted(request.AuthID(), request.AuthRole(), wampcra.Secret, wampcra.Salt,
						wampcra.Iterations, wampcra.KeyLen, 0), nil
				}
				return auth.NewCRAResponse(request.AuthID(), request.AuthRole(), wampcra.Secret, 0), nil
			}
		}
		return nil, fmt.Errorf("invalid realm")

	case auth.MethodCryptoSign:
		cryptosignRequest, ok := request.(*auth.RequestCryptoSign)
		if !ok {
			return nil, fmt.Errorf("invalid request")
		}

		for _, cryptosign := range a.authenticator.CryptoSign {
			if cryptosign.Realm == cryptosignRequest.Realm() &&
				slices.Contains(cryptosign.AuthorizedKeys, cryptosignRequest.PublicKey()) {
				return auth.NewResponse(cryptosignRequest.AuthID(), cryptosignRequest.AuthRole(), 0)
			}
		}
		return nil, fmt.Errorf("unknown publickey")

	default:
		return nil, fmt.Errorf("unknown authentication method: %v", request.AuthMethod())
	}
}
