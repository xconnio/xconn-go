package xconn

import (
	"io"
	"net"
	"time"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
)

type (
	ReaderFunc    func(rw io.ReadWriter) ([]byte, error)
	WriterFunc    func(w io.Writer, p []byte) error
	TransportType int
)

const (
	TransportNone TransportType = iota
	TransportWebSocket
)

var (
	JSONSerializerSpec = NewWSSerializerSpec( //nolint:gochecknoglobals
		JsonWebsocketProtocol, &serializers.JSONSerializer{})
	CBORSerializerSpec = NewWSSerializerSpec( //nolint:gochecknoglobals
		CborWebsocketProtocol, &serializers.CBORSerializer{})
	MsgPackSerializerSpec = NewWSSerializerSpec( //nolint:gochecknoglobals
		MsgpackWebsocketProtocol, &serializers.MsgPackSerializer{})
)

type BaseSession interface {
	ID() int64
	Realm() string
	AuthID() string
	AuthRole() string

	NetConn() net.Conn
	Read() ([]byte, error)
	Write([]byte) error
	Close() error
}

type Peer interface {
	Type() TransportType
	NetConn() net.Conn
	Read() ([]byte, error)
	Write([]byte) error
}

type WSDialerConfig struct {
	SubProtocol string
	DialTimeout time.Duration
}

type WebSocketServerConfig struct {
	SubProtocols []string
}

func DefaultWebSocketServerConfig() *WebSocketServerConfig {
	return &WebSocketServerConfig{
		SubProtocols: []string{
			JsonWebsocketProtocol,
			MsgpackWebsocketProtocol,
			CborWebsocketProtocol,
		},
	}
}

type WSSerializerSpec interface {
	SubProtocol() string
	Serializer() serializers.Serializer
}

type wsSerializerSpec struct {
	subProtocol string
	serializer  serializers.Serializer
}

func (w *wsSerializerSpec) SubProtocol() string {
	return w.subProtocol
}

func (w *wsSerializerSpec) Serializer() serializers.Serializer {
	return w.serializer
}

func NewWSSerializerSpec(subProtocol string, serializer serializers.Serializer) WSSerializerSpec {
	return &wsSerializerSpec{
		subProtocol: subProtocol,
		serializer:  serializer,
	}
}

type Registration struct {
	ID int64
}

type Subscription struct {
	ID int64
}

type Event struct {
	Topic   string
	Args    []any
	KwArgs  map[string]any
	Details map[string]any
}

type Invocation struct {
	Procedure string
	Args      []any
	KwArgs    map[string]any
	Details   map[string]any
}

type Result struct {
	Args    []any
	KwArgs  map[string]any
	Details map[string]any
}

type Error struct {
	URI    string
	Args   []any
	KwArgs map[string]any
}

func (e *Error) Error() string {
	return e.URI
}

type RegisterResponse struct {
	msg   *messages.Registered
	error *Error
}

type CallResponse struct {
	msg   *messages.Result
	error *Error
}

type UnRegisterResponse struct {
	msg   *messages.UnRegistered
	error *Error
}

type SubscribeResponse struct {
	msg   *messages.Subscribed
	error *Error
}

type UnSubscribeResponse struct {
	msg   *messages.UnSubscribed
	error *Error
}

type PublishResponse struct {
	msg   *messages.Published
	error *Error
}
