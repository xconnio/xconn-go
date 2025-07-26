package xconn

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/transports"
)

type Client struct {
	Authenticator  auth.ClientAuthenticator
	SerializerSpec SerializerSpec
	NetDial        func(ctx context.Context, network, addr string) (net.Conn, error)

	DialTimeout       time.Duration
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
}

func (c *Client) Connect(ctx context.Context, url string, realm string) (*Session, error) {
	if c.SerializerSpec == nil {
		c.SerializerSpec = CBORSerializerSpec
	}

	if strings.HasPrefix(url, "ws") {
		joiner := &WebSocketJoiner{
			Authenticator:  c.Authenticator,
			SerializerSpec: c.SerializerSpec,
		}

		dialerConfig := &WSDialerConfig{
			SubProtocol:       c.SerializerSpec.SubProtocol(),
			DialTimeout:       c.DialTimeout,
			NetDial:           c.NetDial,
			KeepAliveInterval: c.KeepAliveInterval,
			KeepAliveTimeout:  c.KeepAliveTimeout,
		}

		base, err := joiner.Join(ctx, url, realm, dialerConfig)
		if err != nil {
			return nil, err
		}

		return NewSession(base, c.SerializerSpec.Serializer()), nil // nolint: contextcheck
	} else if strings.HasPrefix(url, "rs") || strings.HasPrefix(url, "tcp") {
		joiner := &RawSocketJoiner{
			SerializerSpec: c.SerializerSpec,
			Authenticator:  c.Authenticator,
		}
		dialerConfig := &RawSocketDialerConfig{
			Serializer:        transports.Serializer(c.SerializerSpec.SerializerID()),
			DialTimeout:       c.DialTimeout,
			KeepAliveInterval: c.KeepAliveInterval,
			KeepAliveTimeout:  c.KeepAliveTimeout,
		}

		base, err := joiner.Join(ctx, url, realm, dialerConfig)
		if err != nil {
			return nil, err
		}

		return NewSession(base, c.SerializerSpec.Serializer()), nil // nolint: contextcheck
	} else {
		return nil, fmt.Errorf("unsupported protocol: %s", url)
	}
}

func connect(ctx context.Context, url, realm string, authenticator auth.ClientAuthenticator) (*Session, error) {
	client := Client{
		Authenticator: authenticator,
	}

	return client.Connect(ctx, url, realm)
}

func ConnectAnonymous(ctx context.Context, url, realm string) (*Session, error) {
	return connect(ctx, url, realm, nil)
}

func ConnectTicket(ctx context.Context, url, realm, authid, ticket string) (*Session, error) {
	ticketAuthenticator := auth.NewTicketAuthenticator(authid, ticket, nil)

	return connect(ctx, url, realm, ticketAuthenticator)
}

func ConnectCRA(ctx context.Context, url, realm, authid, secret string) (*Session, error) {
	craAuthenticator := auth.NewCRAAuthenticator(authid, secret, nil)

	return connect(ctx, url, realm, craAuthenticator)
}

func ConnectCryptosign(ctx context.Context, url, realm, authid, privateKey string) (*Session, error) {
	cryptosignAuthentication, err := auth.NewCryptoSignAuthenticator(authid, privateKey, nil)
	if err != nil {
		return nil, err
	}

	return connect(ctx, url, realm, cryptosignAuthentication)
}

func ConnectInMemoryBase(router *Router, sessionID uint64, realm, authID, authRole string,
	serializer Serializer) (BaseSession, error) {

	clientPeer, routerPeer := NewInMemoryPeerPair()
	routerSession := NewBaseSession(
		sessionID,
		realm,
		authID,
		authRole,
		routerPeer,
		serializer,
	)

	if err := router.AttachClient(routerSession); err != nil {
		return nil, fmt.Errorf("unable to start local session with ID %v: %w", sessionID, err)
	}

	clientSession := NewBaseSession(
		sessionID,
		realm,
		authID,
		authRole,
		clientPeer,
		serializer,
	)

	return clientSession, nil
}

func ConnectInMemory(router *Router, sessionID uint64, realm, authID, authRole string,
	serializer Serializer) (*Session, error) {

	base, err := ConnectInMemoryBase(router, sessionID, realm, authID, authRole, serializer)
	if err != nil {
		return nil, err
	}

	return NewSession(base, serializer), nil
}
