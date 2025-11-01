package nxt

import (
	"fmt"
	"math/rand"

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
				var authID string
				if anonymous.AuthID == request.AuthID() {
					authID = request.AuthID()
				} else {
					authID = fmt.Sprintf("%012x", rand.Uint64())[:12] // nolint: gosec
				}

				return auth.NewResponse(authID, anonymous.Role, 0)
			}
		}

		return nil, fmt.Errorf("invalid realm")
	case auth.MethodTicket:
		ticketRequest, ok := request.(*auth.TicketRequest)
		if !ok {
			return nil, fmt.Errorf("invalid request")
		}

		for _, ticket := range a.authenticator.Ticket {
			if ticket.Realm == ticketRequest.Realm() &&
				ticket.AuthID == ticketRequest.AuthID() &&
				ticket.Ticket == ticketRequest.Ticket() {
				return auth.NewResponse(ticketRequest.AuthID(), ticket.Role, 0)
			}
		}

		return nil, fmt.Errorf("invalid ticket")
	case auth.MethodCRA:
		for _, wampcra := range a.authenticator.WAMPCRA {
			if wampcra.Realm == request.Realm() && wampcra.AuthID == request.AuthID() {
				if wampcra.Salt != "" {
					return auth.NewCRAResponseSalted(request.AuthID(), wampcra.Role, wampcra.Secret, wampcra.Salt,
						wampcra.Iterations, wampcra.KeyLen, 0), nil
				}
				return auth.NewCRAResponse(request.AuthID(), wampcra.Role, wampcra.Secret, 0), nil
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
				cryptosign.AuthID == cryptosignRequest.AuthID() &&
				slices.Contains(cryptosign.AuthorizedKeys, cryptosignRequest.PublicKey()) {
				return auth.NewResponse(cryptosignRequest.AuthID(), cryptosign.Role, 0)
			}
		}

		return nil, fmt.Errorf("unknown publickey")
	default:
		return nil, fmt.Errorf("unknown authentication method: %v", request.AuthMethod())
	}
}
