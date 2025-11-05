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
	"github.com/xconnio/wampproto-go/transports"
)

var compiledWSProtocols = [][]byte{ //nolint:gochecknoglobals
	[]byte(JsonWebsocketProtocol),
	[]byte(MsgpackWebsocketProtocol),
	[]byte(CborWebsocketProtocol),
}

var wsProtocols = []string{ //nolint:gochecknoglobals
	CborWebsocketProtocol,
	MsgpackWebsocketProtocol,
	JsonWebsocketProtocol,
}

type WebSocketAcceptor struct {
	specs map[string]serializers.Serializer
	once  sync.Once

	Authenticator auth.ServerAuthenticator
}

func (w *WebSocketAcceptor) init() {
	if w.specs == nil {
		w.specs = SerializersByWSSubProtocol()
	}
}

func (w *WebSocketAcceptor) protocols() []string {
	w.once.Do(w.init)

	return maps.Keys(w.specs)
}

func (w *WebSocketAcceptor) RegisterSpec(spec SerializerSpec) error {
	w.once.Do(w.init)

	_, exists := w.specs[spec.SubProtocol()]
	if exists {
		return fmt.Errorf("spec for %s is already registered", spec.SubProtocol())
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

func (w *WebSocketAcceptor) UpgradeAndReadHello(
	conn net.Conn, config *WebSocketServerConfig) (*WebSocketPeer, *messages.Hello, serializers.Serializer, error) {
	if config == nil {
		config = DefaultWebSocketServerConfig()
	}

	config.SubProtocols = w.protocols()
	peer, err := UpgradeWebSocket(conn, config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to init reader/writer: %w", err)
	}

	wsPeer := peer.(*WebSocketPeer)
	serializer, err := w.Spec(wsPeer.Protocol())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("unknown subprotocol: %w", err)
	}

	hello, err := ReadHello(peer, serializer)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read hello: %w", err)
	}

	return wsPeer, hello, serializer, nil
}

func (w *WebSocketAcceptor) Accept(conn net.Conn, router *Router, config *WebSocketServerConfig) (BaseSession, error) {
	peer, hello, serializer, err := w.UpgradeAndReadHello(conn, config)
	if err != nil {
		return nil, err
	}

	if !router.HasRealm(hello.Realm()) {
		abortMessage := messages.NewAbort(map[string]any{}, wampproto.ErrNoSuchRealm, nil, nil)
		serializedAbort, err := serializer.Serialize(abortMessage)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize abort: %w", err)
		}
		if err = peer.Write(serializedAbort); err != nil {
			return nil, fmt.Errorf("failed to send abort: %w", err)
		}

		return nil, fmt.Errorf(wampproto.ErrNoSuchRealm)
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
	details := NewBaseSession(d.ID(), d.Realm(), d.AuthID(), d.AuthRole(), d.AuthMethod(), d.AuthExtra(), peer, serializer)
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
		OutQueueSize:      config.OutQueueSize,
	}
	if peerConfig.OutQueueSize == 0 {
		peerConfig.OutQueueSize = RouterOutQueueSizeDefault
	}

	peer, err := NewWebSocketPeer(conn, peerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to init reader/writer: %w", err)
	}

	return peer, nil
}

var serializersBySerializerID = map[SerializerID]serializers.Serializer{ //nolint:gochecknoglobals
	JsonSerializerID:    &serializers.JSONSerializer{},
	MsgPackSerializerID: &serializers.MsgPackSerializer{},
	CborSerializerID:    &serializers.CBORSerializer{},
}

type RawSocketAcceptor struct {
	specs map[SerializerID]serializers.Serializer
	once  sync.Once

	Authenticator auth.ServerAuthenticator
}

func (r *RawSocketAcceptor) init() {
	if r.specs == nil {
		r.specs = serializersBySerializerID
	}
}

func (r *RawSocketAcceptor) RegisterSpec(spec SerializerSpec) error {
	r.once.Do(r.init)

	_, exists := r.specs[spec.SerializerID()]
	if exists {
		return fmt.Errorf("spec is already registered")
	}

	r.specs[spec.SerializerID()] = spec.Serializer()
	return nil
}

func (r *RawSocketAcceptor) Spec(serializerID SerializerID) (serializers.Serializer, error) {
	r.once.Do(r.init)

	serializer, exists := r.specs[serializerID]
	if !exists {
		return nil, fmt.Errorf("spec for %v is not registered", serializerID)
	}

	return serializer, nil
}

func (r *RawSocketAcceptor) Accept(conn net.Conn, config *RawSocketServerConfig) (BaseSession, error) {
	peer, serializerID, err := UpgradeRawSocket(conn, config)
	if err != nil {
		return nil, fmt.Errorf("failed to init reader/writer: %w", err)
	}

	serializer, err := r.Spec(SerializerID(serializerID))
	if err != nil {
		return nil, fmt.Errorf("failed to init reader/writer: %w", err)
	}

	hello, err := ReadHello(peer, serializer)
	if err != nil {
		return nil, fmt.Errorf("")
	}

	return Accept(peer, hello, serializer, r.Authenticator)
}

func UpgradeRawSocket(conn net.Conn, config *RawSocketServerConfig) (Peer, transports.Serializer, error) {
	maxMessageSize := transports.ProtocolMaxMsgSize

	handshakeRequestRaw := make([]byte, 4)
	_, err := conn.Read(handshakeRequestRaw)
	if err != nil {
		return nil, 0, err
	}

	handshakeRequest, err := transports.ReceiveHandshake(handshakeRequestRaw)
	if err != nil {
		return nil, 0, err
	}

	handshakeResponse := transports.NewHandshake(handshakeRequest.Serializer(), maxMessageSize)
	handshakeResponseRaw, err := transports.SendHandshake(handshakeResponse)
	if err != nil {
		return nil, 0, err
	}

	_, err = conn.Write(handshakeResponseRaw)
	if err != nil {
		return nil, 0, err
	}

	if config == nil {
		config = DefaultRawSocketServerConfig()
	}

	peerConfig := RawSocketPeerConfig{
		Serializer:   handshakeResponse.Serializer(),
		OutQueueSize: config.OutQueueSize,
	}

	if peerConfig.OutQueueSize == 0 {
		peerConfig.OutQueueSize = RouterOutQueueSizeDefault
	}

	return NewRawSocketPeer(conn, peerConfig), handshakeRequest.Serializer(), nil
}
