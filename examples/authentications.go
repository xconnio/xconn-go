package examples

import (
	"context"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/xconn-go"
)

func connect(url, realm string, authenticator auth.ClientAuthenticator, serializerSpec xconn.WSSerializerSpec) (*xconn.Session, error) { // nolint:lll
	client := xconn.Client{Authenticator: authenticator, SerializerSpec: serializerSpec}

	return client.Connect(context.Background(), url, realm)
}

func connectTicket(url, realm, authid, ticket string, serializerSpec xconn.WSSerializerSpec) (*xconn.Session, error) {
	ticketAuthenticator := auth.NewTicketAuthenticator(authid, map[string]any{}, ticket)

	return connect(url, realm, ticketAuthenticator, serializerSpec)
}

func connectCRA(url, realm, authid, secret string, serializerSpec xconn.WSSerializerSpec) (*xconn.Session, error) {
	ticketAuthenticator := auth.NewCRAAuthenticator(authid, map[string]any{}, secret)

	return connect(url, realm, ticketAuthenticator, serializerSpec)
}

func connectCryptosign(url, realm, authid, privateKey string, serializerSpec xconn.WSSerializerSpec) (*xconn.Session, error) { // nolint:lll
	ticketAuthenticator, err := auth.NewCryptoSignAuthenticator(authid, map[string]any{}, privateKey)
	if err != nil {
		return nil, err
	}

	return connect(url, realm, ticketAuthenticator, serializerSpec)
}
