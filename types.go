package xconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/projectdiscovery/ratelimit"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
	"github.com/xconnio/wampproto-go/transports"
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
	TransportRawSocket
)

var (
	JSONSerializerSpec = NewSerializerSpec( //nolint:gochecknoglobals
		JsonWebsocketProtocol, &serializers.JSONSerializer{}, JsonSerializerID)
	CBORSerializerSpec = NewSerializerSpec( //nolint:gochecknoglobals
		CborWebsocketProtocol, &serializers.CBORSerializer{}, CborSerializerID)
	MsgPackSerializerSpec = NewSerializerSpec( //nolint:gochecknoglobals
		MsgpackWebsocketProtocol, &serializers.MsgPackSerializer{}, MsgPackSerializerID)
	ProtobufSerializerSpec = NewSerializerSpec( //nolint:gochecknoglobals
		ProtobufSubProtocol, &wampprotobuf.ProtobufSerializer{}, ProtobufSerializerID)
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

type RawSocketDialerConfig struct {
	Serializer        transports.Serializer
	DialTimeout       time.Duration
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
}

type RawSocketPeerConfig struct {
	Serializer transports.Serializer
}

type ServerConfig struct {
	Throttle          *Throttle
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

type SerializerID transports.Serializer

const (
	JsonSerializerID     SerializerID = 1
	MsgPackSerializerID  SerializerID = 2
	CborSerializerID     SerializerID = 3
	ProtobufSerializerID SerializerID = 15
)

type SerializerSpec interface {
	SubProtocol() string
	Serializer() serializers.Serializer
	SerializerID() SerializerID
}

type serializerSpec struct {
	subProtocol  string
	serializer   serializers.Serializer
	serializerID SerializerID
}

func (w *serializerSpec) SubProtocol() string {
	return w.subProtocol
}

func (w *serializerSpec) Serializer() serializers.Serializer {
	return w.serializer
}

func (w *serializerSpec) SerializerID() SerializerID {
	return w.serializerID
}

func NewSerializerSpec(subProtocol string, serializer serializers.Serializer,
	serializerID SerializerID) SerializerSpec {
	return &serializerSpec{
		subProtocol:  subProtocol,
		serializer:   serializer,
		serializerID: serializerID,
	}
}

type Registration struct {
	id      int64
	session *Session
}

func (r *Registration) Unregister() error {
	if !r.session.Connected() {
		return fmt.Errorf("cannot unregister procedure: session not established")
	}

	unregister := messages.NewUnregister(r.session.idGen.NextID(), r.id)
	toSend, err := r.session.proto.SendMessage(unregister)
	if err != nil {
		return err
	}

	channel := make(chan *UnregisterResponse, 1)
	r.session.unregisterRequests.Store(unregister.RequestID(), channel)
	defer r.session.unregisterRequests.Delete(unregister.RequestID())

	if err = r.session.base.Write(toSend); err != nil {
		return err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return response.error
		}

		r.session.registrations.Delete(r.id)
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("unregister request timed")
	}
}

type Subscription struct {
	id           int64
	session      *Session
	eventHandler EventHandler
}

func (s *Subscription) Unsubscribe() error {
	if !s.session.Connected() {
		return fmt.Errorf("cannot unsubscribe topic: session not established")
	}

	subscriptions, exists := s.session.subscriptions.Load(s.id)
	if exists {
		subs := subscriptions.(map[*Subscription]*Subscription)
		delete(subs, s)
		if len(subs) != 0 {
			s.session.subscriptions.Store(s.id, subs)
			return nil
		}
	}

	unsubscribe := messages.NewUnsubscribe(s.session.idGen.NextID(), s.id)
	toSend, err := s.session.proto.SendMessage(unsubscribe)
	if err != nil {
		return err
	}

	channel := make(chan *UnsubscribeResponse, 1)
	s.session.unsubscribeRequests.Store(unsubscribe.RequestID(), channel)
	defer s.session.unsubscribeRequests.Delete(unsubscribe.RequestID())
	if err = s.session.base.Write(toSend); err != nil {
		return err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return response.error
		}

		s.session.subscriptions.Delete(s.id)
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("unsubscribe request timed")
	}
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

type RegisterRequest struct {
	procedure string
	handler   InvocationHandler
	options   map[string]any
}

func NewRegisterRequest(procedure string, handler InvocationHandler) RegisterRequest {
	return RegisterRequest{
		procedure: procedure,
		handler:   handler,
	}
}

func (r RegisterRequest) Option(key string, value any) RegisterRequest {
	if r.options == nil {
		r.options = make(map[string]any)
	}

	r.options[key] = value
	return r
}

func (r RegisterRequest) Options(options map[string]any) RegisterRequest {
	r.options = options
	return r
}

func (r RegisterRequest) ToRegister(requestID int64) *messages.Register {
	return messages.NewRegister(requestID, r.options, r.procedure)
}

type CallRequest struct {
	procedure string
	args      []any
	kwArgs    map[string]any
	options   map[string]any

	progressReceiver ProgressReceiver
	progressSender   ProgressSender
}

func NewCallRequest(procedure string) CallRequest {
	return CallRequest{procedure: procedure}
}

func (c CallRequest) Option(key string, value any) CallRequest {
	if c.options == nil {
		c.options = make(map[string]any)
	}

	c.options[key] = value
	return c
}

func (c CallRequest) Options(options map[string]any) CallRequest {
	c.options = options
	return c
}

func (c CallRequest) Args(args ...any) CallRequest {
	c.args = args
	return c
}

func (c CallRequest) KWArg(key string, value any) CallRequest {
	if c.kwArgs == nil {
		c.kwArgs = make(map[string]any)
	}

	c.kwArgs[key] = value
	return c
}

func (c CallRequest) KWArgs(kwArgs map[string]any) CallRequest {
	c.kwArgs = kwArgs
	return c
}

func (c CallRequest) ProgressReceiver(handler ProgressReceiver) CallRequest {
	c.progressReceiver = handler
	return c
}

func (c CallRequest) ProgressSender(handler ProgressSender) CallRequest {
	c.progressSender = handler
	return c
}

func (c CallRequest) ToCall(requestID int64) *messages.Call {
	return messages.NewCall(requestID, c.options, c.procedure, c.args, c.kwArgs)
}

func (c CallRequest) Validate() error {
	if c.procedure == "" {
		return errors.New("procedure is required")
	}

	return nil
}

type SubscribeRequest struct {
	topic   string
	handler EventHandler
	options map[string]any
}

func NewSubscribeRequest(topic string, handler EventHandler) SubscribeRequest {
	return SubscribeRequest{
		topic:   topic,
		handler: handler,
	}
}

func (r SubscribeRequest) Option(key string, value any) SubscribeRequest {
	if r.options == nil {
		r.options = make(map[string]any)
	}

	r.options[key] = value
	return r
}

func (r SubscribeRequest) Options(options map[string]any) SubscribeRequest {
	r.options = options
	return r
}

func (r SubscribeRequest) ToSubscribe(requestID int64) *messages.Subscribe {
	return messages.NewSubscribe(requestID, r.options, r.topic)
}

type PublishRequest struct {
	topic   string
	args    []any
	kwArgs  map[string]any
	options map[string]any
}

func NewPublishRequest(topic string) PublishRequest {
	return PublishRequest{topic: topic}
}

func (p PublishRequest) Option(key string, value any) PublishRequest {
	if p.options == nil {
		p.options = make(map[string]any)
	}

	p.options[key] = value
	return p
}

func (p PublishRequest) Options(options map[string]any) PublishRequest {
	p.options = options
	return p
}

func (p PublishRequest) Args(args ...any) PublishRequest {
	p.args = args
	return p
}

func (p PublishRequest) KWArg(key string, value any) PublishRequest {
	if p.kwArgs == nil {
		p.kwArgs = make(map[string]any)
	}

	p.kwArgs[key] = value
	return p
}

func (p PublishRequest) KWArgs(kwArgs map[string]any) PublishRequest {
	p.kwArgs = kwArgs
	return p
}

func (p PublishRequest) ToPublish(requestID int64) *messages.Publish {
	return messages.NewPublish(requestID, p.options, p.topic, p.args, p.kwArgs)
}

func (p PublishRequest) Validate() error {
	if p.topic == "" {
		return errors.New("topic is required")
	}

	return nil
}

type Strategy int

const (
	Burst Strategy = iota // Process all requests instantly up to the limit
	LeakyBucket
)

type Throttle struct {
	rate     uint          // max messages in duration
	duration time.Duration // duration for the rate
	strategy Strategy      // strategy for handling throttle
}

func NewThrottle(rate uint, duration time.Duration, strategy Strategy) *Throttle {
	return &Throttle{
		rate:     rate,
		duration: duration,
		strategy: strategy,
	}
}

func (t *Throttle) Create() *ratelimit.Limiter {
	if t.strategy == LeakyBucket {
		return ratelimit.NewLeakyBucket(context.Background(), t.rate, t.duration)
	}
	return ratelimit.New(context.Background(), t.rate, t.duration)
}
