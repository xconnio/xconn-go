package xconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
	wampprotobuf "github.com/xconnio/wampproto-protobuf/go"
	"github.com/xconnio/xconn-go/internal"
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
	SubProtocol       string
	DialTimeout       time.Duration
	NetDial           func(ctx context.Context, network, addr string) (net.Conn, error)
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
}

type WebSocketServerConfig struct {
	SubProtocols      []string
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
}

type WSPeerConfig struct {
	Protocol          string
	Binary            bool
	Server            bool
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
}

type ServerConfig struct {
	Throttle          *internal.Throttle
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
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
	errStr := e.URI
	if e.Arguments != nil {
		args := make([]string, len(e.Arguments))
		for i, arg := range e.Arguments {
			args[i] = fmt.Sprintf("%v", arg)
		}
		errStr += ": " + strings.Join(args, ", ")
	}

	if e.KwArguments != nil {
		kwargs := make([]string, len(e.KwArguments))
		for key, value := range e.KwArguments {
			kwargs = append(kwargs, fmt.Sprintf("%s=%v", key, value))
		}
		errStr += ": " + strings.Join(kwargs, ", ")
	}

	return errStr
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

type CallRequest struct {
	procedure string
	args      []any
	kwArgs    map[string]any
	options   map[string]any

	progressReceiver ProgressHandler
	progressSender   SendProgressive
}

func NewCallRequest(procedure string) CallRequest {
	return CallRequest{procedure: procedure}
}

func (c CallRequest) WithOption(key string, value any) CallRequest {
	if c.options == nil {
		c.options = make(map[string]any)
	}

	c.options[key] = value
	return c
}

func (c CallRequest) WithOptions(options map[string]any) CallRequest {
	c.options = options
	return c
}

func (c CallRequest) WithArgs(args ...any) CallRequest {
	c.args = args
	return c
}

func (c CallRequest) WithKWArg(key string, value any) CallRequest {
	if c.kwArgs == nil {
		c.kwArgs = make(map[string]any)
	}

	c.kwArgs[key] = value
	return c
}

func (c CallRequest) WithKWArgs(kwArgs map[string]any) CallRequest {
	c.kwArgs = kwArgs
	return c
}

func (c CallRequest) WithProgressReceiver(handler ProgressHandler) CallRequest {
	c.progressReceiver = handler
	return c
}

func (c CallRequest) WithProgressSender(handler SendProgressive) CallRequest {
	c.progressSender = handler
	return c
}

func (c CallRequest) Options() map[string]any {
	return c.options
}

func (c CallRequest) KWArgs() map[string]any {
	return c.kwArgs
}

func (c CallRequest) Args() []any {
	return c.args
}

func (c CallRequest) Procedure() string {
	return c.procedure
}

func (c CallRequest) ProgressReceiver() ProgressHandler {
	return c.progressReceiver
}

func (c CallRequest) ProgressSender() SendProgressive {
	return c.progressSender
}

func (c CallRequest) Validate() error {
	if c.procedure == "" {
		return errors.New("procedure is required")
	}

	return nil
}
