package xconn

import (
	"bytes"
	"fmt"
	"net"
	"sync"

	"github.com/gobwas/ws"
	"golang.org/x/exp/maps"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
)

var compiledWSProtocols = [][]byte{ //nolint:gochecknoglobals
	[]byte(JsonWebsocketProtocol),
	[]byte(MsgpackWebsocketProtocol),
	[]byte(CborWebsocketProtocol),
}

var serializersByWSSubProtocol = map[string]serializers.Serializer{ //nolint:gochecknoglobals
	JsonWebsocketProtocol:    &serializers.JSONSerializer{},
	MsgpackWebsocketProtocol: &serializers.MsgPackSerializer{},
	CborWebsocketProtocol:    &serializers.CBORSerializer{},
}

type WebSocketAcceptor struct {
	specs map[string]serializers.Serializer
	once  sync.Once

	Authenticator auth.ServerAuthenticator
}

func (w *WebSocketAcceptor) init() {
	if w.specs == nil {
		w.specs = serializersByWSSubProtocol
	}
}

func (w *WebSocketAcceptor) protocols() []string {
	w.once.Do(w.init)

	return maps.Keys(w.specs)
}

func (w *WebSocketAcceptor) RegisterSpec(spec WSSerializerSpec) error {
	w.once.Do(w.init)

	_, exists := w.specs[spec.SubProtocol()]
	if exists {
		return fmt.Errorf("spec for %s is alraedy registered", spec.SubProtocol())
	}

	w.specs[spec.SubProtocol()] = spec.Serializer()
	return nil
}

func (w *WebSocketAcceptor) Spec(subProtocol string) (serializers.Serializer, error) {
	w.once.Do(w.init)

	serializer, exists := w.specs[subProtocol]
	if !exists {
		return nil, fmt.Errorf("spec for %s is not registered", subProtocol)
	}

	return serializer, nil
}

func (w *WebSocketAcceptor) Accept(conn net.Conn, config *WebSocketServerConfig) (BaseSession, error) {
	config.SubProtocols = w.protocols()
	peer, err := UpgradeWebSocket(conn, config)
	if err != nil {
		return nil, fmt.Errorf("failed to init reader/writer: %w", err)
	}

	wsPeer := peer.(*WebSocketPeer)
	serializer, err := w.Spec(wsPeer.Protocol())
	if err != nil {
		return nil, fmt.Errorf("unknown subprotocol: %w", err)
	}

	hello, err := ReadHello(peer, serializer)
	if err != nil {
		return nil, fmt.Errorf("")
	}

	return Accept(peer, hello, serializer, w.Authenticator)
}

func Accept(peer Peer, hello *messages.Hello, serializer serializers.Serializer,
	authenticator auth.ServerAuthenticator) (BaseSession, error) {

	a := wampproto.NewAcceptor(serializer, authenticator)
	toSend, err := a.ReceiveMessage(hello)
	if err != nil {
		return nil, err
	}

	if err = WriteMessage(peer, toSend, serializer); err != nil {
		return nil, err
	}

	if toSend.Type() == messages.MessageTypeWelcome {
		goto Welcomed
	}

	for {
		payload, err := peer.Read()
		if err != nil {
			return nil, err
		}

		toSend, welcomed, err := a.Receive(payload)
		if err != nil {
			return nil, err
		}

		if err = peer.Write(toSend); err != nil {
			return nil, err
		}

		if welcomed {
			goto Welcomed
		}
	}

Welcomed:
	d, _ := a.SessionDetails()
	details := NewBaseSession(d.ID(), d.Realm(), d.AuthID(), d.AuthRole(), peer, serializer)
	return details, nil
}

func UpgradeWebSocket(conn net.Conn, config *WebSocketServerConfig) (Peer, error) {
	wsUpgrader := ws.Upgrader{
		Protocol: func(protoBytes []byte) bool {
			if config == nil {
				for _, protocol := range compiledWSProtocols {
					if bytes.Equal(protoBytes, protocol) {
						return true
					}
				}
			} else {
				for _, protocol := range config.SubProtocols {
					if bytes.Equal(protoBytes, []byte(protocol)) {
						return true
					}
				}
			}

			return false
		},
	}

	hs, err := wsUpgrader.Upgrade(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to upgrade to websocket: %w", err)
	}

	isBinary := hs.Protocol != JsonWebsocketProtocol

	peerConfig := WSPeerConfig{
		Protocol:          hs.Protocol,
		Binary:            isBinary,
		Server:            true,
		KeepAliveInterval: config.KeepAliveInterval,
		KeepAliveTimeout:  config.KeepAliveTimeout,
	}
	peer, err := NewWebSocketPeer(conn, peerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to init reader/writer: %w", err)
	}

	return peer, nil
}
