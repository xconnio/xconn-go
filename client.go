package xconn

import (
	"context"
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"

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
	OutQueueSize      int
}

func (c *Client) Connect(ctx context.Context, uri string, realm string) (*Session, error) {
	if strings.HasPrefix(uri, "ws://") || strings.HasPrefix(uri, "wss://") || strings.HasPrefix(uri, "unix+ws://") {
		subprotocols := wsProtocols
		if c.SerializerSpec != nil {
			subprotocols = []string{c.SerializerSpec.SubProtocol()}
		}

		dialerConfig := &WSDialerConfig{
			SubProtocols:      subprotocols,
			DialTimeout:       c.DialTimeout,
			NetDial:           c.NetDial,
			KeepAliveInterval: c.KeepAliveInterval,
			KeepAliveTimeout:  c.KeepAliveTimeout,
			OutQueueSize:      c.OutQueueSize,
		}

		joiner := &WebSocketJoiner{
			Authenticator:  c.Authenticator,
			SerializerSpec: c.SerializerSpec,
		}

		base, err := joiner.Join(ctx, uri, realm, dialerConfig)
		if err != nil {
			return nil, err
		}

		return NewSession(base, base.Serializer()), nil // nolint: contextcheck
	} else if strings.HasPrefix(uri, "rs://") || strings.HasPrefix(uri, "rss://") ||
		strings.HasPrefix(uri, "unix://") || strings.HasPrefix(uri, "unix+rs://") {
		if c.SerializerSpec == nil {
			c.SerializerSpec = CBORSerializerSpec
		}

		dialerConfig := &RawSocketDialerConfig{
			Serializer:        transports.Serializer(c.SerializerSpec.SerializerID()),
			DialTimeout:       c.DialTimeout,
			NetDial:           c.NetDial,
			KeepAliveInterval: c.KeepAliveInterval,
			KeepAliveTimeout:  c.KeepAliveTimeout,
			OutQueueSize:      c.OutQueueSize,
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
	craAuthenticator := auth.NewWAMPCRAAuthenticator(authid, secret, nil)

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
	serializer serializers.Serializer, outQueueSize int) (BaseSession, error) {

	if serializer == nil {
		return nil, fmt.Errorf("serializer must not be nil")
	}

	clientPeer, routerPeer := NewInMemoryPeerPair(outQueueSize)
	sessionID := wampproto.GenerateID()
	routerSession := NewBaseSession(
		sessionID,
		realm,
		authID,
		authRole,
		"",
		map[string]any{},
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
		"",
		map[string]any{},
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
			}
		}
	}()

	return clientSession, nil
}

func ConnectInMemory(router *Router, realm string) (*Session, error) {
	authID := fmt.Sprintf("%012x", rand.Uint64())[:12] // #nosec
	authRole := "trusted"
	outQueueSize := 16

	base, err := ConnectInMemoryBase(router, realm, authID, authRole, &serializers.MsgPackSerializer{}, outQueueSize)
	if err != nil {
		return nil, err
	}

	return NewSession(base, base.Serializer()), nil
}

// ConnectQUIC connects to a WAMP router over QUIC and opens the first WAMP session.
// Additional sessions can be opened on the same connection via QUICSession.OpenSession.
func ConnectQUIC(ctx context.Context, address, realm string, config *QUICDialerConfig) (*QUICSession, error) {
	if config == nil {
		config = &QUICDialerConfig{}
	}

	tlsConf := config.TLSConfig
	if tlsConf == nil {
		tlsConf = &tls.Config{NextProtos: []string{NextProtoWAMP}}
	} else if !slices.Contains(tlsConf.NextProtos, NextProtoWAMP) {
		tlsConf = tlsConf.Clone()
		tlsConf.NextProtos = append(tlsConf.NextProtos, NextProtoWAMP)
	}

	dialCtx := ctx
	if config.DialTimeout != 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, config.DialTimeout)
		defer cancel()
	}

	quicCfg := DefaultQuicConfig()
	if config.KeepAlivePeriod > 0 {
		quicCfg.KeepAlivePeriod = config.KeepAlivePeriod
	}
	rawConn, err := quic.DialAddr(dialCtx, address, tlsConf, quicCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", address, err)
	}

	quicConn := &QUICConn{conn: rawConn}
	sess, err := openQUICSession(ctx, quicConn, realm, config)
	if err != nil {
		_ = rawConn.CloseWithError(0, "")
		return nil, err
	}
	return sess, nil
}
