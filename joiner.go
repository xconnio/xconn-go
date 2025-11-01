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
	"github.com/xconnio/wampproto-go/transports"
)

type WebSocketJoiner struct {
	SerializerSpec SerializerSpec
	Authenticator  auth.ClientAuthenticator
}

type RawSocketJoiner struct {
	SerializerSpec SerializerSpec
	Authenticator  auth.ClientAuthenticator
}

func (w *WebSocketJoiner) Join(ctx context.Context, url, realm string, config *WSDialerConfig) (BaseSession, error) {
	parsedURL, err := netURL.Parse(url)
	if err != nil {
		return nil, err
	}

	if w.Authenticator == nil {
		w.Authenticator = auth.NewAnonymousAuthenticator("", nil)
	}

	peer, subprotocol, err := DialWebSocket(ctx, parsedURL, config)
	if err != nil {
		return nil, err
	}

	var serializer serializers.Serializer
	if w.SerializerSpec != nil {
		serializer = w.SerializerSpec.Serializer()
	} else {
		serializer = SerializersByWSSubProtocol()[subprotocol]
	}

	return Join(peer, realm, serializer, w.Authenticator)
}

func DialWebSocket(ctx context.Context, url *netURL.URL, config *WSDialerConfig) (Peer, string, error) {
	if config == nil {
		config = &WSDialerConfig{SubProtocols: wsProtocols}
	}

	wsDialer := ws.Dialer{
		Protocols: config.SubProtocols,
	}

	if config.DialTimeout == 0 {
		wsDialer.Timeout = time.Second * 10
	} else {
		wsDialer.Timeout = config.DialTimeout
	}

	if config.NetDial == nil {
		if url.Scheme == "unix+ws" {
			// Custom dial function for Unix Domain Socket
			wsDialer.NetDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial(string(NetworkUnix), url.Path)
			}

			url.Scheme = "ws"
			url.Host = string(NetworkUnix)
		}
	} else {
		wsDialer.NetDial = config.NetDial
	}

	conn, _, hs, err := wsDialer.Dial(ctx, url.String())
	if err != nil {
		return nil, "", err
	}

	isBinary := hs.Protocol != JsonWebsocketProtocol

	peerConfig := WSPeerConfig{
		Protocol:          hs.Protocol,
		Binary:            isBinary,
		Server:            false,
		KeepAliveInterval: config.KeepAliveInterval,
		KeepAliveTimeout:  config.KeepAliveTimeout,
		OutQueueSize:      config.OutQueueSize,
	}
	wsPeer, err := NewWebSocketPeer(conn, peerConfig)
	if err != nil {
		return nil, "", err
	}

	return wsPeer, hs.Protocol, nil
}

func (r *RawSocketJoiner) Join(ctx context.Context, uri, realm string,
	config *RawSocketDialerConfig) (BaseSession, error) {
	parsedURL, err := netURL.Parse(uri)
	if err != nil {
		return nil, err
	}

	if r.Authenticator == nil {
		r.Authenticator = auth.NewAnonymousAuthenticator("", nil)
	}

	peer, err := DialRawSocket(ctx, parsedURL, config)
	if err != nil {
		return nil, err
	}

	return Join(peer, realm, r.SerializerSpec.Serializer(), r.Authenticator)
}

func DialRawSocket(ctx context.Context, url *netURL.URL, config *RawSocketDialerConfig) (Peer, error) {
	var dialer net.Dialer
	var conn net.Conn
	var err error
	if url.Scheme == "unix" || url.Scheme == "unix+rs" {
		conn, err = dialer.DialContext(ctx, "unix", url.Path)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", url.Host)
	}

	if err != nil {
		return nil, err
	}

	header := transports.NewHandshake(config.Serializer, transports.DefaultMaxMsgSize)
	headerRaw, err := transports.SendHandshake(header)
	if err != nil {
		return nil, err
	}

	_, err = conn.Write(headerRaw)
	if err != nil {
		return nil, err
	}

	responseHeader := make([]byte, 4)
	_, err = conn.Read(responseHeader)
	if err != nil {
		return nil, err
	}

	_, err = transports.ReceiveHandshake(responseHeader)
	if err != nil {
		return nil, err
	}

	return NewRawSocketPeer(conn, RawSocketPeerConfig{
		Serializer:   config.Serializer,
		OutQueueSize: config.OutQueueSize,
	}), nil
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
