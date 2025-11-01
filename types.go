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
	TryWrite([]byte) (bool, error)
	ReadMessage() (messages.Message, error)
	WriteMessage(messages.Message) error
	TryWriteMessage(messages.Message) (bool, error)
	EnableLogPublishing(session *Session, topic string)
	DisableLogPublishing()
	Close() error
}

type Peer interface {
	Type() TransportType
	NetConn() net.Conn
	Read() ([]byte, error)
	Write([]byte) error
	TryWrite([]byte) (bool, error)
	Close() error
}

type WSDialerConfig struct {
	SubProtocols      []string
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
	NetDial           func(ctx context.Context, network, addr string) (net.Conn, error)
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
	JsonSerializerID    SerializerID = 1
	MsgPackSerializerID SerializerID = 2
	CborSerializerID    SerializerID = 3
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
	toSend, err := r.session.serializer.Serialize(unregister)
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
	toSend, err := s.session.serializer.Serialize(unsubscribe)
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

type SendProgress func(args []any, kwargs map[string]any) error

type InvocationResult struct {
	Args    []any
	Kwargs  map[string]any
	Details map[string]any
	Err     string
}

func NewInvocationResult(args ...any) *InvocationResult {
	if len(args) == 0 {
		return &InvocationResult{}
	}

	return &InvocationResult{
		Args: args,
	}
}

func NewInvocationError(uri string, args ...any) *InvocationResult {
	if len(args) == 0 {
		return &InvocationResult{Err: uri}
	}

	return &InvocationResult{
		Err:  uri,
		Args: args,
	}
}

type Progress struct {
	Args    []any
	Kwargs  map[string]any
	Options map[string]any
	Err     error
}

func NewProgress(args ...any) *Progress {
	return &Progress{
		Args:    args,
		Options: map[string]any{wampproto.OptionProgress: true},
	}
}

func NewFinalProgress(args ...any) *Progress {
	return &Progress{
		Args: args,
	}
}

type ProgressResult struct {
	args    List
	kwargs  Dict
	details Dict
}

func (p *ProgressResult) ArgUInt64(index int) (uint64, error) {
	return p.args.UInt64(index)
}

func (p *ProgressResult) ArgUInt64Or(index int, def uint64) uint64 {
	return p.args.UInt64Or(index, def)
}

func (p *ProgressResult) ArgString(index int) (string, error) {
	return p.args.String(index)
}

func (p *ProgressResult) ArgStringOr(index int, def string) string {
	return p.args.StringOr(index, def)
}

func (p *ProgressResult) ArgBool(index int) (bool, error) {
	return p.args.Bool(index)
}

func (p *ProgressResult) ArgBoolOr(index int, def bool) bool {
	return p.args.BoolOr(index, def)
}

func (p *ProgressResult) ArgFloat64(index int) (float64, error) {
	return p.args.Float64(index)
}

func (p *ProgressResult) ArgFloat64Or(index int, def float64) float64 {
	return p.args.Float64Or(index, def)
}

func (p *ProgressResult) ArgInt64(index int) (int64, error) {
	return p.args.Int64(index)
}

func (p *ProgressResult) ArgInt64Or(index int, def int64) int64 {
	return p.args.Int64Or(index, def)
}

func (p *ProgressResult) ArgBytes(index int) ([]byte, error) {
	return p.args.Bytes(index)
}

func (p *ProgressResult) ArgsStruct(out any) error {
	return p.args.Decode(out)
}

func (p *ProgressResult) ArgBytesOr(index int, def []byte) []byte {
	return p.args.BytesOr(index, def)
}

func (p *ProgressResult) ArgList(index int) (List, error) {
	return p.args.List(index)
}

func (p *ProgressResult) ArgListOr(index int, def List) List {
	return p.args.ListOr(index, def)
}

func (p *ProgressResult) ArgDict(index int) (map[string]any, error) {
	return p.args.Dict(index)
}

func (p *ProgressResult) ArgDictOr(index int, def map[string]any) map[string]any {
	return p.args.DictOr(index, def)
}

func (p *ProgressResult) ArgsLen() int {
	return p.args.Len()
}

func (p *ProgressResult) Args() []any {
	return p.args.Raw()
}

func (p *ProgressResult) KwargUInt64(key string) (uint64, error) {
	return p.kwargs.UInt64(key)
}

func (p *ProgressResult) KwargUInt64Or(key string, def uint64) uint64 {
	return p.kwargs.UInt64Or(key, def)
}

func (p *ProgressResult) KwargString(key string) (string, error) {
	return p.kwargs.String(key)
}

func (p *ProgressResult) KwargStringOr(key string, def string) string {
	return p.kwargs.StringOr(key, def)
}

func (p *ProgressResult) KwargBool(key string) (bool, error) {
	return p.kwargs.Bool(key)
}

func (p *ProgressResult) KwargBoolOr(key string, def bool) bool {
	return p.kwargs.BoolOr(key, def)
}

func (p *ProgressResult) KwargFloat64(key string) (float64, error) {
	return p.kwargs.Float64(key)
}

func (p *ProgressResult) KwargFloat64Or(key string, def float64) float64 {
	return p.kwargs.Float64Or(key, def)
}

func (p *ProgressResult) KwargInt64(key string) (int64, error) {
	return p.kwargs.Int64(key)
}

func (p *ProgressResult) KwargInt64Or(key string, def int64) int64 {
	return p.kwargs.Int64Or(key, def)
}

func (p *ProgressResult) KwargBytes(key string) ([]byte, error) {
	return p.kwargs.Bytes(key)
}

func (p *ProgressResult) KwargBytesOr(key string, def []byte) []byte {
	return p.kwargs.BytesOr(key, def)
}

func (p *ProgressResult) KwargList(key string) (List, error) {
	return p.kwargs.List(key)
}

func (p *ProgressResult) KwargListOr(key string, def List) List {
	return p.kwargs.ListOr(key, def)
}

func (p *ProgressResult) KwargDict(key string) (map[string]any, error) {
	return p.kwargs.Dict(key)
}

func (p *ProgressResult) KwargDictOr(key string, def map[string]any) map[string]any {
	return p.kwargs.DictOr(key, def)
}

func (p *ProgressResult) KwargsStruct(out any) error {
	return p.kwargs.Decode(out)
}

func (p *ProgressResult) KwargsLen() int {
	return p.kwargs.Len()
}

func (p *ProgressResult) Kwargs() map[string]any {
	return p.kwargs.Raw()
}

func (p *ProgressResult) Details() map[string]any {
	return p.details.Raw()
}

type Error struct {
	URI    string
	Args   []any
	Kwargs map[string]any
}

func (e *Error) Error() string {
	errStr := e.URI
	if e.Args != nil {
		args := make([]string, len(e.Args))
		for i, arg := range e.Args {
			args[i] = fmt.Sprintf("%v", arg)
		}
		errStr += ": " + strings.Join(args, ", ")
	}

	if e.Kwargs != nil {
		kwargs := make([]string, len(e.Kwargs))
		for key, value := range e.Kwargs {
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

func (r *RegisterRequest) Match(value string) *RegisterRequest {
	if r.options == nil {
		r.options = make(map[string]any)
	}

	r.options[wampproto.OptionMatch] = value
	return r
}

func (r *RegisterRequest) Invoke(value string) *RegisterRequest {
	if r.options == nil {
		r.options = make(map[string]any)
	}

	r.options["invoke"] = value
	return r
}

func (r *RegisterRequest) ToRegister(requestID uint64) *messages.Register {
	return messages.NewRegister(requestID, r.options, r.procedure)
}

type CallRequest struct {
	session *Session

	procedure string
	args      []any
	kwargs    map[string]any
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

func (c *CallRequest) Kwarg(key string, value any) *CallRequest {
	if c.kwargs == nil {
		c.kwargs = make(map[string]any)
	}

	c.kwargs[key] = value
	return c
}

func (c *CallRequest) Kwargs(kwargs map[string]any) *CallRequest {
	c.kwargs = kwargs
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

func (r *SubscribeRequest) Match(value string) *SubscribeRequest {
	if r.options == nil {
		r.options = make(map[string]any)
	}

	r.options[wampproto.OptionMatch] = value
	return r
}

func (r *SubscribeRequest) ToSubscribe(requestID uint64) *messages.Subscribe {
	return messages.NewSubscribe(requestID, r.options, r.topic)
}

type PublishRequest struct {
	session *Session

	topic   string
	args    []any
	kwargs  map[string]any
	options map[string]any
}

func (p *PublishRequest) Do() PublishResponse {
	return p.session.publish(p.topic, p.args, p.kwargs, p.options)
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

func (p *PublishRequest) Acknowledge(value bool) *PublishRequest {
	if p.options == nil {
		p.options = make(map[string]any)
	}

	p.options["acknowledge"] = value
	return p
}

func (p *PublishRequest) ExcludeMe(value bool) *PublishRequest {
	if p.options == nil {
		p.options = make(map[string]any)
	}

	p.options["exclude_me"] = value
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

func (p *PublishRequest) Kwarg(key string, value any) *PublishRequest {
	if p.kwargs == nil {
		p.kwargs = make(map[string]any)
	}

	p.kwargs[key] = value
	return p
}

func (p *PublishRequest) Kwargs(kwArgs map[string]any) *PublishRequest {
	p.kwargs = kwArgs
	return p
}

func (p *PublishRequest) ToPublish(requestID uint64) *messages.Publish {
	return messages.NewPublish(requestID, p.options, p.topic, p.args, p.kwargs)
}

type CallResponse struct {
	args    List
	kwargs  Dict
	details Dict
	Err     error
}

func (c *CallResponse) ArgUInt64(index int) (uint64, error) {
	return c.args.UInt64(index)
}

func (c *CallResponse) ArgUInt64Or(index int, def uint64) uint64 {
	return c.args.UInt64Or(index, def)
}

func (c *CallResponse) ArgString(index int) (string, error) {
	return c.args.String(index)
}

func (c *CallResponse) ArgStringOr(index int, def string) string {
	return c.args.StringOr(index, def)
}

func (c *CallResponse) ArgBool(index int) (bool, error) {
	return c.args.Bool(index)
}

func (c *CallResponse) ArgBoolOr(index int, def bool) bool {
	return c.args.BoolOr(index, def)
}

func (c *CallResponse) ArgFloat64(index int) (float64, error) {
	return c.args.Float64(index)
}

func (c *CallResponse) ArgFloat64Or(index int, def float64) float64 {
	return c.args.Float64Or(index, def)
}

func (c *CallResponse) ArgInt64(index int) (int64, error) {
	return c.args.Int64(index)
}

func (c *CallResponse) ArgInt64Or(index int, def int64) int64 {
	return c.args.Int64Or(index, def)
}

func (c *CallResponse) ArgBytes(index int) ([]byte, error) {
	return c.args.Bytes(index)
}

func (c *CallResponse) ArgsStruct(out any) error {
	return c.args.Decode(out)
}

func (c *CallResponse) ArgBytesOr(index int, def []byte) []byte {
	return c.args.BytesOr(index, def)
}

func (c *CallResponse) ArgList(index int) (List, error) {
	return c.args.List(index)
}

func (c *CallResponse) ArgListOr(index int, def List) List {
	return c.args.ListOr(index, def)
}

func (c *CallResponse) ArgDict(index int) (map[string]any, error) {
	return c.args.Dict(index)
}

func (c *CallResponse) ArgDictOr(index int, def map[string]any) map[string]any {
	return c.args.DictOr(index, def)
}

func (c *CallResponse) ArgsLen() int {
	return c.args.Len()
}

func (c *CallResponse) Args() []any {
	return c.args.Raw()
}

func (c *CallResponse) KwargUInt64(key string) (uint64, error) {
	return c.kwargs.UInt64(key)
}

func (c *CallResponse) KwargUInt64Or(key string, def uint64) uint64 {
	return c.kwargs.UInt64Or(key, def)
}

func (c *CallResponse) KwargString(key string) (string, error) {
	return c.kwargs.String(key)
}

func (c *CallResponse) KwargStringOr(key string, def string) string {
	return c.kwargs.StringOr(key, def)
}

func (c *CallResponse) KwargBool(key string) (bool, error) {
	return c.kwargs.Bool(key)
}

func (c *CallResponse) KwargBoolOr(key string, def bool) bool {
	return c.kwargs.BoolOr(key, def)
}

func (c *CallResponse) KwargFloat64(key string) (float64, error) {
	return c.kwargs.Float64(key)
}

func (c *CallResponse) KwargFloat64Or(key string, def float64) float64 {
	return c.kwargs.Float64Or(key, def)
}

func (c *CallResponse) KwargInt64(key string) (int64, error) {
	return c.kwargs.Int64(key)
}

func (c *CallResponse) KwargInt64Or(key string, def int64) int64 {
	return c.kwargs.Int64Or(key, def)
}

func (c *CallResponse) KwargBytes(key string) ([]byte, error) {
	return c.kwargs.Bytes(key)
}

func (c *CallResponse) KwargBytesOr(key string, def []byte) []byte {
	return c.kwargs.BytesOr(key, def)
}

func (c *CallResponse) KwargsStruct(out any) error {
	return c.kwargs.Decode(out)
}

func (c *CallResponse) KwargsList(key string) (List, error) {
	return c.kwargs.List(key)
}

func (c *CallResponse) KwargListOr(key string, def List) List {
	return c.kwargs.ListOr(key, def)
}

func (c *CallResponse) KwargDict(key string) (map[string]any, error) {
	return c.kwargs.Dict(key)
}

func (c *CallResponse) KwargDictOr(key string, def map[string]any) map[string]any {
	return c.kwargs.DictOr(key, def)
}

func (c *CallResponse) KwargsLen() int {
	return c.kwargs.Len()
}

func (c *CallResponse) Kwargs() map[string]any {
	return c.kwargs.Raw()
}

func (c *CallResponse) Details() map[string]any {
	return c.details.Raw()
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
	return wampproto.NewSessionDetails(id, realm, authID, authRole, false, wampproto.RouterRoles)
}

type Authorizer interface {
	Authorize(baseSession BaseSession, msg messages.Message) (bool, error)
}
