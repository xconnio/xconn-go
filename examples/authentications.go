package examples

import (
	"context"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/xconn-go"
)

func connect(url, realm string, authenticator auth.ClientAuthenticator, serializerSpec xconn.SerializerSpec) (*xconn.Session, error) { // nolint:lll
	client := xconn.Client{Authenticator: authenticator, SerializerSpec: serializerSpec}

	return client.Connect(context.Background(), url, realm)
}

func connectTicket(url, realm, authid, ticket string, serializerSpec xconn.SerializerSpec) (*xconn.Session, error) {
	ticketAuthenticator := auth.NewTicketAuthenticator(authid, ticket, map[string]any{})

	return connect(url, realm, ticketAuthenticator, serializerSpec)
}

func connectWAMPCRA(url, realm, authid, secret string, serializerSpec xconn.SerializerSpec) (*xconn.Session, error) {
	ticketAuthenticator := auth.NewWAMPCRAAuthenticator(authid, secret, map[string]any{})

	return connect(url, realm, ticketAuthenticator, serializerSpec)
}

func connectCryptosign(url, realm, authid, privateKey string, serializerSpec xconn.SerializerSpec) (*xconn.Session, error) { // nolint:lll
	ticketAuthenticator, err := auth.NewCryptoSignAuthenticator(authid, privateKey, map[string]any{})
	if err != nil {
		return nil, err
	}

	return connect(url, realm, ticketAuthenticator, serializerSpec)
}
