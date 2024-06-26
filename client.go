package xconn

import (
	"context"
	"time"

	"github.com/xconnio/wampproto-go/auth"
)

type Client struct {
	Authenticator  auth.ClientAuthenticator
	SerializerSpec WSSerializerSpec

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
	}

	base, err := joiner.Join(ctx, url, realm)
	if err != nil {
		return nil, err
	}

	return NewSession(base, c.SerializerSpec.Serializer()), nil // nolint: contextcheck
}
