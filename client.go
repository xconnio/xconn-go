package xconn

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/xconnio/wampproto-go/auth"
)

type Client struct {
	Authenticator  auth.ClientAuthenticator
	SerializerSpec WSSerializerSpec
	NetDial        func(ctx context.Context, network, addr string) (net.Conn, error)

	DialTimeout       time.Duration
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
}

func (c *Client) Connect(ctx context.Context, url string, realm string) (*Session, error) {
	if c.SerializerSpec == nil {
		c.SerializerSpec = JSONSerializerSpec
	}

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
}

func Connect(ctx context.Context, url string, realm string) (*Session, error) {
	if strings.HasPrefix(url, "ws") {
		joiner := &WebSocketJoiner{}
		dialerConfig := &WSDialerConfig{
			SubProtocol: JsonWebsocketProtocol,
		}
		base, err := joiner.Join(ctx, url, realm, dialerConfig)
		if err != nil {
			return nil, err
		}

		return NewSession(base, JSONSerializerSpec.Serializer()), nil // nolint: contextcheck
	} else if strings.HasPrefix(url, "rs") || strings.HasPrefix(url, "tcp") {
		joiner := &RawSocketJoiner{}
		dialerConfig := &RawSocketDialerConfig{}
		base, err := joiner.Join(ctx, url, realm, dialerConfig)
		if err != nil {
			return nil, err
		}

		return NewSession(base, CBORSerializerSpec.Serializer()), nil // nolint: contextcheck
	} else {
		return nil, fmt.Errorf("unsupported protocol: %s", url)
	}
}
