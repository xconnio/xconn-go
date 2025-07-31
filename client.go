package xconn

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/serializers"
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

func (c *Client) Connect(ctx context.Context, uri string, realm string) (*Session, error) {
	if c.SerializerSpec == nil {
		c.SerializerSpec = CBORSerializerSpec
	}

	if strings.HasPrefix(uri, "ws://") || strings.HasPrefix(uri, "wss://") || strings.HasPrefix(uri, "unix+ws://") {
		dialerConfig := &WSDialerConfig{
			SubProtocol:       c.SerializerSpec.SubProtocol(),
			DialTimeout:       c.DialTimeout,
			NetDial:           c.NetDial,
			KeepAliveInterval: c.KeepAliveInterval,
			KeepAliveTimeout:  c.KeepAliveTimeout,
		}

		joiner := &WebSocketJoiner{
			Authenticator:  c.Authenticator,
			SerializerSpec: c.SerializerSpec,
		}

		base, err := joiner.Join(ctx, uri, realm, dialerConfig)
		if err != nil {
			return nil, err
		}

		return NewSession(base, c.SerializerSpec.Serializer()), nil // nolint: contextcheck
	} else if strings.HasPrefix(uri, "rs://") || strings.HasPrefix(uri, "rss://") ||
		strings.HasPrefix(uri, "unix://") || strings.HasPrefix(uri, "unix+rs://") {
		dialerConfig := &RawSocketDialerConfig{
			Serializer:        transports.Serializer(c.SerializerSpec.SerializerID()),
			DialTimeout:       c.DialTimeout,
			NetDial:           c.NetDial,
			KeepAliveInterval: c.KeepAliveInterval,
			KeepAliveTimeout:  c.KeepAliveTimeout,
		}

		joiner := &RawSocketJoiner{
			SerializerSpec: c.SerializerSpec,
			Authenticator:  c.Authenticator,
		}

		base, err := joiner.Join(ctx, uri, realm, dialerConfig)
		if err != nil {
			return nil, err
		}

		return NewSession(base, c.SerializerSpec.Serializer()), nil // nolint: contextcheck
	} else {
		return nil, fmt.Errorf("unsupported protocol: %s", uri)
	}
}

func connect(ctx context.Context, uri, realm string, authenticator auth.ClientAuthenticator) (*Session, error) {
	client := Client{
		Authenticator: authenticator,
	}

	return client.Connect(ctx, uri, realm)
}

func ConnectAnonymous(ctx context.Context, uri, realm string) (*Session, error) {
	return connect(ctx, uri, realm, nil)
}

func ConnectTicket(ctx context.Context, uri, realm, authid, ticket string) (*Session, error) {
	ticketAuthenticator := auth.NewTicketAuthenticator(authid, ticket, nil)

	return connect(ctx, uri, realm, ticketAuthenticator)
}

func ConnectCRA(ctx context.Context, uri, realm, authid, secret string) (*Session, error) {
	craAuthenticator := auth.NewCRAAuthenticator(authid, secret, nil)

	return connect(ctx, uri, realm, craAuthenticator)
}

func ConnectCryptosign(ctx context.Context, uri, realm, authid, privateKey string) (*Session, error) {
	cryptosignAuthentication, err := auth.NewCryptoSignAuthenticator(authid, privateKey, nil)
	if err != nil {
		return nil, err
	}

	return connect(ctx, uri, realm, cryptosignAuthentication)
}

func ConnectInMemoryBase(router *Router, realm, authID, authRole string,
	serializer serializers.Serializer) (BaseSession, error) {

	if serializer == nil {
		return nil, fmt.Errorf("serializer must not be nil")
	}

	clientPeer, routerPeer := NewInMemoryPeerPair()
	sessionID := wampproto.GenerateID()
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

	go func() {
		for {
			msg, err := routerSession.ReadMessage()
			if err != nil {
				_ = router.DetachClient(routerSession)
				_ = routerSession.Close()
				return
			}

			if err = router.ReceiveMessage(routerSession, msg); err != nil {
				log.Println(err)
				return
			}
		}
	}()

	return clientSession, nil
}

func ConnectInMemory(router *Router, realm string) (*Session, error) {
	authID := fmt.Sprintf("%012x", rand.Uint64())[:12] // #nosec
	authRole := "trusted"

	base, err := ConnectInMemoryBase(router, realm, authID, authRole, &serializers.MsgPackSerializer{})
	if err != nil {
		return nil, err
	}

	return NewSession(base, base.Serializer()), nil
}
