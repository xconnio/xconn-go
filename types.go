package xconn

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
	wampprotobuf "github.com/xconnio/wampproto-protobuf/go"
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
	ProtobufSerializerSpec = NewWSSerializerSpec( //nolint:gochecknoglobals
		ProtobufSubProtocol, &wampprotobuf.ProtobufSerializer{})
)

type BaseSession interface {
	ID() int64
	Realm() string
	AuthID() string
	AuthRole() string

	Serializer() serializers.Serializer
	NetConn() net.Conn
	Read() ([]byte, error)
	Write([]byte) error
	ReadMessage() (messages.Message, error)
	WriteMessage(messages.Message) error
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
	NetDial     func(ctx context.Context, network, addr string) (net.Conn, error)
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
	Topic       string
	Arguments   []any
	KwArguments map[string]any
	Details     map[string]any
}

type SendProgress func(arguments []any, kwArguments map[string]any) error

type Invocation struct {
	Procedure   string
	Arguments   []any
	KwArguments map[string]any
	Details     map[string]any

	SendProgress SendProgress
}

type Result struct {
	Arguments   []any
	KwArguments map[string]any
	Details     map[string]any
	Err         string
}

type Progress struct {
	Arguments   []any
	KwArguments map[string]any
	Options     map[string]any
	Err         error
}

type Error struct {
	URI         string
	Arguments   []any
	KwArguments map[string]any
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

type UnregisterResponse struct {
	msg   *messages.Unregistered
	error *Error
}

type SubscribeResponse struct {
	msg   *messages.Subscribed
	error *Error
}

type UnsubscribeResponse struct {
	msg   *messages.Unsubscribed
	error *Error
}

type PublishResponse struct {
	msg   *messages.Published
	error *Error
}

type GoodBye struct {
	Details map[string]any
	Reason  string
}
