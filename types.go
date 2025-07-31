package xconn

import (
	"context"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"time"

	"github.com/projectdiscovery/ratelimit"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
	"github.com/xconnio/wampproto-go/transports"
	wampprotobuf "github.com/xconnio/wampproto-protobuf/go"
)

type (
	ReaderFunc    func(rw io.ReadWriter) ([]byte, error)
	WriterFunc    func(w io.Writer, p []byte) error
	TransportType int

	Serializer serializers.Serializer
)

const (
	TransportNone TransportType = iota
	TransportWebSocket
	TransportRawSocket
	TransportInMemory
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
	ID() uint64
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
	id      uint64
	session *Session
}

func (r *Registration) unregister() error {
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
	id           uint64
	session      *Session
	eventHandler EventHandler
}

func (s *Subscription) unsubscribe() error {
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

type registerResponse struct {
	msg   *messages.Registered
	error *Error
}

type callResponse struct {
	msg   *messages.Result
	error *Error
}

type UnregisterResponse struct {
	msg   *messages.Unregistered
	error *Error
}

type subscribeResponse struct {
	msg   *messages.Subscribed
	error *Error
}

type UnsubscribeResponse struct {
	msg   *messages.Unsubscribed
	error *Error
}

type publishResponse struct {
	msg   *messages.Published
	error *Error
}

type GoodBye struct {
	Details map[string]any
	Reason  string
}

type RegisterRequest struct {
	session *Session

	procedure string
	handler   InvocationHandler
	options   map[string]any
}

func (r *RegisterRequest) Do() RegisterResponse {
	return r.session.register(r.procedure, r.handler, r.options)
}

func (r *RegisterRequest) Option(key string, value any) *RegisterRequest {
	if r.options == nil {
		r.options = make(map[string]any)
	}

	r.options[key] = value
	return r
}

func (r *RegisterRequest) Options(options map[string]any) *RegisterRequest {
	r.options = options
	return r
}

func (r *RegisterRequest) ToRegister(requestID uint64) *messages.Register {
	return messages.NewRegister(requestID, r.options, r.procedure)
}

type CallRequest struct {
	session *Session

	procedure string
	args      []any
	kwArgs    map[string]any
	options   map[string]any

	progressReceiver ProgressReceiver
	progressSender   ProgressSender
}

func (c *CallRequest) Do() CallResponse {
	return c.DoContext(context.Background())
}

func (c *CallRequest) DoContext(ctx context.Context) CallResponse {
	return c.session.callWithRequest(ctx, c)
}

func (c *CallRequest) Option(key string, value any) *CallRequest {
	if c.options == nil {
		c.options = make(map[string]any)
	}

	c.options[key] = value
	return c
}

func (c *CallRequest) Options(options map[string]any) *CallRequest {
	c.options = options
	return c
}

func (c *CallRequest) Arg(arg any) *CallRequest {
	if c.args == nil {
		c.args = make([]any, 0)
	}

	c.args = append(c.args, arg)
	return c
}

func (c *CallRequest) Args(args ...any) *CallRequest {
	c.args = args
	return c
}

func (c *CallRequest) KWArg(key string, value any) *CallRequest {
	if c.kwArgs == nil {
		c.kwArgs = make(map[string]any)
	}

	c.kwArgs[key] = value
	return c
}

func (c *CallRequest) KWArgs(kwArgs map[string]any) *CallRequest {
	c.kwArgs = kwArgs
	return c
}

func (c *CallRequest) ProgressReceiver(handler ProgressReceiver) *CallRequest {
	c.progressReceiver = handler
	return c
}

func (c *CallRequest) ProgressSender(handler ProgressSender) *CallRequest {
	c.progressSender = handler
	return c
}

func (c *CallRequest) ToCall(requestID uint64) *messages.Call {
	return messages.NewCall(requestID, c.options, c.procedure, c.args, c.kwArgs)
}

type SubscribeRequest struct {
	session *Session

	topic   string
	handler EventHandler
	options map[string]any
}

func (r *SubscribeRequest) Do() SubscribeResponse {
	return r.session.subscribe(r.topic, r.handler, r.options)
}

func (r *SubscribeRequest) Option(key string, value any) *SubscribeRequest {
	if r.options == nil {
		r.options = make(map[string]any)
	}

	r.options[key] = value
	return r
}

func (r *SubscribeRequest) Options(options map[string]any) *SubscribeRequest {
	r.options = options
	return r
}

func (r *SubscribeRequest) ToSubscribe(requestID uint64) *messages.Subscribe {
	return messages.NewSubscribe(requestID, r.options, r.topic)
}

type PublishRequest struct {
	session *Session

	topic   string
	args    []any
	kwArgs  map[string]any
	options map[string]any
}

func (p *PublishRequest) Do() PublishResponse {
	return p.session.publish(p.topic, p.args, p.kwArgs, p.options)
}

func (p *PublishRequest) Option(key string, value any) *PublishRequest {
	if p.options == nil {
		p.options = make(map[string]any)
	}

	p.options[key] = value
	return p
}

func (p *PublishRequest) Options(options map[string]any) *PublishRequest {
	p.options = options
	return p
}

func (p *PublishRequest) Arg(arg any) *PublishRequest {
	if p.args == nil {
		p.args = make([]any, 0)
	}

	p.args = append(p.args, arg)
	return p
}

func (p *PublishRequest) Args(args ...any) *PublishRequest {
	p.args = args
	return p
}

func (p *PublishRequest) KWArg(key string, value any) *PublishRequest {
	if p.kwArgs == nil {
		p.kwArgs = make(map[string]any)
	}

	p.kwArgs[key] = value
	return p
}

func (p *PublishRequest) KWArgs(kwArgs map[string]any) *PublishRequest {
	p.kwArgs = kwArgs
	return p
}

func (p *PublishRequest) ToPublish(requestID uint64) *messages.Publish {
	return messages.NewPublish(requestID, p.options, p.topic, p.args, p.kwArgs)
}

type CallResponse struct {
	Arguments   []any
	KwArguments map[string]any
	Details     map[string]any
	Err         error
}

type RegisterResponse struct {
	registration *Registration
	Err          error
}

func (r RegisterResponse) Unregister() error {
	return r.registration.unregister()
}

type SubscribeResponse struct {
	subscription *Subscription
	Err          error
}

func (r SubscribeResponse) Unsubscribe() error {
	return r.subscription.unsubscribe()
}

type PublishResponse struct {
	Err error
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

func wildcardMatch(str, pattern string) bool {
	matched, err := path.Match(pattern, str)
	return err == nil && matched
}

type Permission struct {
	URI            string
	MatchPolicy    string
	AllowCall      bool
	AllowPublish   bool
	AllowRegister  bool
	AllowSubscribe bool
}

func (p Permission) Allows(msgType uint64) bool {
	switch msgType {
	case messages.MessageTypeCall:
		return p.AllowCall
	case messages.MessageTypeRegister:
		return p.AllowRegister
	case messages.MessageTypeSubscribe:
		return p.AllowSubscribe
	case messages.MessageTypePublish:
		return p.AllowPublish
	default:
		return false
	}
}

func (p Permission) MatchURI(uri string) bool {
	switch p.MatchPolicy {
	case "", wampproto.MatchExact:
		return uri == p.URI
	case wampproto.MatchPrefix:
		return strings.HasPrefix(uri, p.URI)
	case wampproto.MatchWildcard:
		return wildcardMatch(uri, p.URI)
	default:
		return false
	}
}

type RealmRole struct {
	Name        string
	Permissions []Permission
}

type SessionDetails = wampproto.SessionDetails

func NewSessionDetails(id uint64, realm, authID, authRole string) *SessionDetails {
	return wampproto.NewSessionDetails(id, realm, authID, authRole, false)
}
