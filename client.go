package xconn

import (
	"context"
	"net"
	"time"

	"github.com/xconnio/wampproto-go/auth"
)

type Client struct {
	Authenticator  auth.ClientAuthenticator
	SerializerSpec WSSerializerSpec
	NetDial        func(ctx context.Context, network, addr string) (net.Conn, error)

	DialTimeout time.Duration
}

func (c *Client) Connect(ctx context.Context, url string, realm string) (*Session, error) {
	if c.SerializerSpec == nil {
		c.SerializerSpec = JSONSerializerSpec
	}

	joiner := &WebSocketJoiner{
		Authenticator:  c.Authenticator,
		SerializerSpec: c.SerializerSpec,
		DialTimeout:    c.DialTimeout,
		NetDial:        c.NetDial,
	}

	base, err := joiner.Join(ctx, url, realm)
	if err != nil {
		return nil, err
	}

	return NewSession(base, c.SerializerSpec.Serializer()), nil // nolint: contextcheck
}
