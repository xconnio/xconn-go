package xconn

import (
	"context"
	"fmt"
	"net"
	netURL "net/url"
	"time"

	"github.com/gobwas/ws"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/serializers"
)

type WebSocketJoiner struct {
	SerializerSpec WSSerializerSpec
	Authenticator  auth.ClientAuthenticator

	DialTimeout time.Duration
}

func (w *WebSocketJoiner) Join(ctx context.Context, url, realm string) (BaseSession, error) {
	parsedURL, err := netURL.Parse(url)
	if err != nil {
		return nil, err
	}

	if w.SerializerSpec == nil {
		w.SerializerSpec = JSONSerializerSpec
	}

	if w.Authenticator == nil {
		w.Authenticator = auth.NewAnonymousAuthenticator("", nil)
	}

	if w.DialTimeout == 0 {
		w.DialTimeout = time.Second * 10
	}

	dialConfig := WSDialerConfig{
		SubProtocol: w.SerializerSpec.SubProtocol(),
		DialTimeout: w.DialTimeout,
	}

	peer, err := DialWebSocket(ctx, parsedURL, dialConfig)
	if err != nil {
		return nil, err
	}

	return Join(peer, realm, w.SerializerSpec.Serializer(), w.Authenticator)
}

func DialWebSocket(ctx context.Context, url *netURL.URL, config WSDialerConfig) (Peer, error) {
	wsDialer := ws.Dialer{
		Protocols: []string{config.SubProtocol},
	}

	if config.DialTimeout == 0 {
		wsDialer.Timeout = time.Second * 10
	} else {
		wsDialer.Timeout = config.DialTimeout
	}

	if url.Scheme == "unix" {
		// Custom dial function for Unix Domain Socket
		wsDialer.NetDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("unix", url.Path)
		}
		url.Scheme = "ws"
	}

	conn, _, _, err := wsDialer.Dial(ctx, url.String())
	if err != nil {
		return nil, err
	}

	isBinary := config.SubProtocol != JsonWebsocketProtocol
	return NewWebSocketPeer(conn, config.SubProtocol, isBinary, false)
}

func Join(cl Peer, realm string, serializer serializers.Serializer,
	authenticator auth.ClientAuthenticator) (BaseSession, error) {

	j := wampproto.NewJoiner(realm, serializer, authenticator)
	hello, err := j.SendHello()
	if err != nil {
		return nil, err
	}

	if err = cl.Write(hello); err != nil {
		return nil, fmt.Errorf("failed to send wamp hello: %w", err)
	}

	for {
		msg, err := cl.Read()
		if err != nil {
			return nil, fmt.Errorf("failed to parse websocket: %w", err)
		}

		toSend, err := j.Receive(msg)
		if err != nil {
			return nil, err
		}

		// nothing to send, this means the proto was established.
		if toSend == nil {
			details, err := j.SessionDetails()
			if err != nil {
				return nil, err
			}

			base := NewBaseSession(details.ID(), details.Realm(), details.AuthID(), details.AuthRole(), cl, serializer)
			return base, nil
		}

		if err = cl.Write(toSend); err != nil {
			return nil, fmt.Errorf("failed to send message: %w", err)
		}
	}
}
