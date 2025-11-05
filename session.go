package xconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
	"github.com/xconnio/wampproto-go/util"
)

const ErrNoResult = "io.xconn.no_result"

type InvocationHandler func(ctx context.Context, invocation *Invocation) *InvocationResult
type EventHandler func(event *Event)
type ProgressReceiver func(result *ProgressResult)
type ProgressSender func(ctx context.Context) *Progress

type Session struct {
	base       BaseSession
	details    *SessionDetails
	serializer serializers.Serializer

	// wamp id generator
	idGen *wampproto.SessionScopeIDGenerator

	// remote procedure calls data structures
	registerRequests   sync.Map
	unregisterRequests sync.Map
	registrations      sync.Map
	callRequests       sync.Map
	progressHandlers   sync.Map
	progressFunc       sync.Map

	// publish subscribe data structures
	subscribeRequests   sync.Map
	unsubscribeRequests sync.Map
	subscriptions       sync.Map
	publishRequests     sync.Map

	goodbyeChan chan struct{}
	goodBye     *GoodBye

	connected bool
	onLeave   func()
	leaveChan chan struct{}

	sync.Mutex
}

func NewSession(base BaseSession, serializer serializers.Serializer) *Session {
	session := &Session{
		base:       base,
		idGen:      &wampproto.SessionScopeIDGenerator{},
		serializer: serializer,

		registerRequests:   sync.Map{},
		unregisterRequests: sync.Map{},
		registrations:      sync.Map{},
		callRequests:       sync.Map{},
		progressHandlers:   sync.Map{},
		progressFunc:       sync.Map{},

		subscribeRequests:   sync.Map{},
		unsubscribeRequests: sync.Map{},
		subscriptions:       sync.Map{},
		publishRequests:     sync.Map{},

		goodbyeChan: make(chan struct{}, 1),

		leaveChan: make(chan struct{}, 1),

		details: NewSessionDetails(base.ID(), base.Realm(), base.AuthID(), base.AuthRole(), base.AuthMethod(),
			base.AuthExtra()),
		connected: true,
	}

	go session.waitForRouterMessages()
	return session
}

func (s *Session) waitForRouterMessages() {
	for {
		payload, err := s.base.Read()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Println("failed to read message: ", err)
			}

			s.markDisconnected()
			_ = s.base.Close()
			return
		}

		msg, err := s.serializer.Deserialize(payload)
		if err != nil {
			log.Println("failed to parse message: ", err)
			_ = s.base.Close()
			return
		}

		if err = s.processIncomingMessage(msg); err != nil {
			log.Println("failed to process router message: ", err)
			return
		}
	}
}

func (s *Session) processIncomingMessage(msg messages.Message) error {
	switch msg.Type() {
	case messages.MessageTypeRegistered:
		registered := msg.(*messages.Registered)
		request, exists := s.registerRequests.Load(registered.RequestID())
		if !exists {
			return fmt.Errorf("received REGISTERED for unknown request")
		}

		requestChan := request.(chan *registerResponse)
		requestChan <- &registerResponse{msg: registered}
	case messages.MessageTypeUnregistered:
		unregistered := msg.(*messages.Unregistered)
		request, exists := s.unregisterRequests.Load(unregistered.RequestID())
		if !exists {
			return fmt.Errorf("received UNREGISTERED for unknown request")
		}

		requestChan := request.(chan *UnregisterResponse)
		requestChan <- &UnregisterResponse{msg: unregistered}
	case messages.MessageTypeResult:
		result := msg.(*messages.Result)
		request, exists := s.callRequests.Load(result.RequestID())
		if !exists {
			return fmt.Errorf("received RESULT for unknown request")
		}

		progress, _ := result.Details()[wampproto.OptionProgress].(bool)
		if progress {
			progressHandler, exists := s.progressHandlers.Load(result.RequestID())
			if exists {
				progHandler := progressHandler.(ProgressReceiver)
				progHandler(&ProgressResult{
					args:    NewList(result.Args()),
					kwargs:  NewDict(result.KwArgs()),
					details: NewDict(result.Details()),
				})
			}
		} else {
			req := request.(chan *callResponse)
			req <- &callResponse{msg: result}
			s.progressHandlers.Delete(result.RequestID())
		}

	case messages.MessageTypeInvocation:
		invocation := msg.(*messages.Invocation)
		end, _ := s.registrations.Load(invocation.RegistrationID())
		endpoint := end.(InvocationHandler)

		inv := NewInvocation(invocation.Args(), invocation.KwArgs(), invocation.Details())
		progress, _ := invocation.Details()[wampproto.OptionProgress].(bool)
		receiveProgress, _ := invocation.Details()[wampproto.OptionReceiveProgress].(bool)
		if receiveProgress {
			progressFunc := func(args []any, kwArgs map[string]any) error {
				yield := messages.NewYield(invocation.RequestID(), map[string]any{"progress": true}, args, kwArgs)
				payload, err := s.serializer.Serialize(yield)
				if err != nil {
					return fmt.Errorf("failed to send yield: %w", err)
				}

				if err = s.base.Write(payload); err != nil {
					return fmt.Errorf("failed to send yield: %w", err)
				}
				return nil
			}
			inv.SendProgress = progressFunc

			if progress {
				s.progressFunc.Store(invocation.RequestID(), progressFunc)
			}
		}

		if progress && !receiveProgress {
			progressFunc, ok := s.progressFunc.Load(invocation.RequestID())
			if ok {
				inv.SendProgress = progressFunc.(func(args []any, kwArgs map[string]any) error)
			}
		}

		if !progress && !receiveProgress {
			s.progressFunc.Delete(invocation.RequestID())
		}

		go func() {
			var msgToSend messages.Message
			res := endpoint(context.Background(), inv)
			if res.Err == ErrNoResult {
				return
			} else if res.Err != "" {
				msgToSend = messages.NewError(
					invocation.Type(), invocation.RequestID(), map[string]any{}, res.Err, res.Args, res.Kwargs,
				)
			} else {
				msgToSend = messages.NewYield(invocation.RequestID(), nil, res.Args, res.Kwargs)
			}

			payload, err := s.serializer.Serialize(msgToSend)
			if err != nil {
				log.Println("failed to send yield: %w", err)
				return
			}

			if err = s.base.Write(payload); err != nil {
				log.Println("failed to send yield: %w", err)
				return
			}
		}()

	case messages.MessageTypeSubscribed:
		subscribed := msg.(*messages.Subscribed)
		request, exists := s.subscribeRequests.Load(subscribed.RequestID())
		if !exists {
			return fmt.Errorf("received SUBSCRIBED for unknown request")
		}

		req := request.(chan *subscribeResponse)
		req <- &subscribeResponse{msg: subscribed}
	case messages.MessageTypeUnsubscribed:
		unsubscribed := msg.(*messages.Unsubscribed)
		request, exists := s.unsubscribeRequests.Load(unsubscribed.RequestID())
		if !exists {
			return fmt.Errorf("received UNSUBSCRIBED for unknown request")
		}

		req := request.(chan *UnsubscribeResponse)
		req <- &UnsubscribeResponse{msg: unsubscribed}
	case messages.MessageTypePublished:
		published := msg.(*messages.Published)
		request, exists := s.publishRequests.Load(published.RequestID())
		if !exists {
			return fmt.Errorf("received PUBLISHED for unknown request")
		}

		req := request.(chan *publishResponse)
		req <- &publishResponse{msg: published}
	case messages.MessageTypeEvent:
		event := msg.(*messages.Event)
		subscriptions, exists := s.subscriptions.Load(event.SubscriptionID())
		if !exists {
			return fmt.Errorf("received EVENT for unknown subscription")
		}

		evt := NewEvent(event.Args(), event.KwArgs(), event.Details())
		subs := subscriptions.(map[*Subscription]*Subscription)
		for _, sub := range subs {
			go sub.eventHandler(evt)
		}
	case messages.MessageTypeError:
		errorMsg := msg.(*messages.Error)
		switch errorMsg.MessageType() {
		case messages.MessageTypeCall:
			response, exists := s.callRequests.LoadAndDelete(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid call request")
			}

			err := &Error{URI: errorMsg.URI(), Args: errorMsg.Args(), Kwargs: errorMsg.KwArgs()}
			responseChan := response.(chan *callResponse)
			responseChan <- &callResponse{error: err}
			return nil
		case messages.MessageTypeRegister:
			request, exists := s.registerRequests.LoadAndDelete(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid register request")
			}

			err := &Error{URI: errorMsg.URI(), Args: errorMsg.Args(), Kwargs: errorMsg.KwArgs()}
			requestChan := request.(chan *registerResponse)
			requestChan <- &registerResponse{error: err}
			return nil
		case messages.MessageTypeUnregister:
			_, exists := s.unregisterRequests.LoadAndDelete(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid unregister request")
			}

			return nil
		case messages.MessageTypeSubscribe:
			response, exists := s.subscribeRequests.LoadAndDelete(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid subscribe request")
			}

			err := &Error{URI: errorMsg.URI(), Args: errorMsg.Args(), Kwargs: errorMsg.KwArgs()}
			responseChan := response.(chan *subscribeResponse)
			responseChan <- &subscribeResponse{error: err}
			return nil
		case messages.MessageTypeUnsubscribe:
			_, exists := s.unsubscribeRequests.Load(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid unsubscribe request")
			}

			s.unsubscribeRequests.Delete(errorMsg.RequestID())
			return nil
		case messages.MessageTypePublish:
			response, exists := s.publishRequests.LoadAndDelete(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid publish request")
			}

			err := &Error{URI: errorMsg.URI(), Args: errorMsg.Args(), Kwargs: errorMsg.KwArgs()}
			responseChan := response.(chan *publishResponse)
			responseChan <- &publishResponse{error: err}
			return nil
		default:
			return fmt.Errorf("unknown error message type %T", msg)
		}
	case messages.MessageTypeGoodbye:
		goodByeMessage := msg.(*messages.GoodBye)
		s.goodBye = &GoodBye{
			Details: goodByeMessage.Details(),
			Reason:  goodByeMessage.Reason(),
		}

		s.goodbyeChan <- struct{}{}
		if s.onLeave != nil {
			s.onLeave()
		}
		s.markDisconnected()
	case messages.MessageTypeAbort:
		s.markDisconnected()
	default:
		return fmt.Errorf("SESSION: received unexpected message %T", msg)
	}

	return nil
}

func (s *Session) Connected() bool {
	s.Lock()
	defer s.Unlock()

	return s.connected
}

func (s *Session) ID() uint64 {
	return s.base.ID()
}

func (s *Session) Register(procedure string, handler InvocationHandler) *RegisterRequest {
	return &RegisterRequest{session: s, procedure: procedure, handler: handler}
}

func (s *Session) register(procedure string, handler InvocationHandler,
	options map[string]any) RegisterResponse {
	if !s.Connected() {
		return RegisterResponse{Err: fmt.Errorf("cannot register procedure: session not established")}
	}

	register := messages.NewRegister(s.idGen.NextID(), options, procedure)
	toSend, err := s.serializer.Serialize(register)
	if err != nil {
		return RegisterResponse{Err: err}
	}

	channel := make(chan *registerResponse, 1)
	s.registerRequests.Store(register.RequestID(), channel)
	defer s.registerRequests.Delete(register.RequestID())

	if err = s.base.Write(toSend); err != nil {
		return RegisterResponse{Err: err}
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return RegisterResponse{Err: response.error}
		}

		s.registrations.Store(response.msg.RegistrationID(), handler)
		registration := &Registration{
			id:      response.msg.RegistrationID(),
			session: s,
		}
		return RegisterResponse{registration: registration}
	case <-time.After(10 * time.Second):
		return RegisterResponse{Err: fmt.Errorf("register request timed out")}
	}
}

func (s *Session) call(ctx context.Context, call *messages.Call) CallResponse {
	if !s.Connected() {
		return CallResponse{Err: fmt.Errorf("cannot call procedure: session not established")}
	}

	toSend, err := s.serializer.Serialize(call)
	if err != nil {
		return CallResponse{Err: err}
	}

	channel := make(chan *callResponse, 1)
	s.callRequests.Store(call.RequestID(), channel)
	defer s.callRequests.Delete(call.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return CallResponse{Err: err}
	}

	return s.waitForCallResult(ctx, channel)
}

func (s *Session) Call(procedure string) *CallRequest {
	return &CallRequest{session: s, procedure: procedure}
}

func (s *Session) callRaw(ctx context.Context, procedure string, args []any, kwArgs map[string]any,
	options map[string]any) CallResponse {

	var call *messages.Call
	rawPayload, _ := util.AsBool(options["x_payload_raw"])
	if rawPayload {
		delete(options, "x_payload_raw")
		if len(args) > 1 {
			return CallResponse{Err: fmt.Errorf("must provide at most one argument when x_payload_raw is set")}
		}

		if len(kwArgs) != 0 {
			return CallResponse{Err: fmt.Errorf("must not provide kwargs when x_payload_raw is set")}
		}

		if len(args) == 0 {
			call = messages.NewCallBinary(s.idGen.NextID(), options, procedure, nil, 0)
		} else {
			payload, ok := args[0].([]byte)
			if !ok {
				return CallResponse{Err: fmt.Errorf("argument must be a byte array when x_payload_raw is set")}
			}

			call = messages.NewCallBinary(s.idGen.NextID(), options, procedure, payload, 0)
		}
	} else {
		call = messages.NewCall(s.idGen.NextID(), options, procedure, args, kwArgs)
	}

	return s.call(ctx, call)
}

func (s *Session) callProgress(ctx context.Context, procedure string, args []any, kwArgs map[string]any,
	options map[string]any, progressHandler ProgressReceiver) CallResponse {

	call := messages.NewCall(s.idGen.NextID(), options, procedure, args, kwArgs)
	if progressHandler == nil {
		progressHandler = func(result *ProgressResult) {}
	}
	s.progressHandlers.Store(call.RequestID(), progressHandler)
	call.Options()[wampproto.OptionReceiveProgress] = true

	return s.call(ctx, call)
}

func (s *Session) callProgressive(ctx context.Context, procedure string,
	progressFunc ProgressSender) CallResponse {
	progress := progressFunc(ctx)
	if progress.Err != nil {
		return CallResponse{Err: progress.Err}
	}
	call := messages.NewCall(s.idGen.NextID(), progress.Options, procedure, progress.Args, progress.Kwargs)

	toSend, err := s.serializer.Serialize(call)
	if err != nil {
		return CallResponse{Err: err}
	}

	channel := make(chan *callResponse, 1)
	s.callRequests.Store(call.RequestID(), channel)
	defer s.callRequests.Delete(call.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return CallResponse{Err: err}
	}

	callInProgress, _ := progress.Options[wampproto.OptionProgress].(bool)
	go func() {
		for callInProgress {
			prog := progressFunc(ctx)
			if prog.Err != nil {
				// TODO: implement call canceling
				return
			}
			call = messages.NewCall(call.RequestID(), prog.Options, procedure, prog.Args, prog.Kwargs)
			toSend, err = s.serializer.Serialize(call)
			if err != nil {
				return
			}
			if err = s.base.Write(toSend); err != nil {
				return
			}

			callInProgress, _ = prog.Options[wampproto.OptionProgress].(bool)
		}
	}()

	return s.waitForCallResult(ctx, channel)
}

func (s *Session) callProgressiveProgress(ctx context.Context, procedure string,
	progressFunc ProgressSender, progressHandler ProgressReceiver) CallResponse {

	if progressHandler == nil {
		progressHandler = func(result *ProgressResult) {}
	}

	progress := progressFunc(ctx)
	if progress.Err != nil {
		return CallResponse{Err: progress.Err}
	}
	call := messages.NewCall(s.idGen.NextID(), progress.Options, procedure, progress.Args, progress.Kwargs)
	s.progressHandlers.Store(call.RequestID(), progressHandler)
	call.Options()[wampproto.OptionReceiveProgress] = true

	toSend, err := s.serializer.Serialize(call)
	if err != nil {
		return CallResponse{Err: err}
	}

	channel := make(chan *callResponse, 1)
	s.callRequests.Store(call.RequestID(), channel)
	defer s.callRequests.Delete(call.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return CallResponse{Err: err}
	}

	callInProgress, _ := progress.Options[wampproto.OptionProgress].(bool)
	go func() {
		for callInProgress {
			prog := progressFunc(ctx)
			if prog.Err != nil {
				// TODO: implement call canceling
				return
			}
			call := messages.NewCall(call.RequestID(), prog.Options, procedure, prog.Args, prog.Kwargs)
			toSend, err = s.serializer.Serialize(call)
			if err != nil {
				return
			}
			if err = s.base.Write(toSend); err != nil {
				return
			}

			callInProgress, _ = prog.Options[wampproto.OptionProgress].(bool)
		}
	}()

	return s.waitForCallResult(ctx, channel)
}

func (s *Session) callWithRequest(ctx context.Context, request *CallRequest) CallResponse {
	switch {
	case request.progressSender == nil && request.progressReceiver == nil:
		return s.callRaw(ctx, request.procedure, request.args, request.kwargs, request.options)
	case request.progressSender != nil && request.progressReceiver == nil:
		return s.callProgressive(ctx, request.procedure, request.progressSender)
	case request.progressSender == nil && request.progressReceiver != nil:
		return s.callProgress(ctx, request.procedure, request.args, request.kwargs, request.options,
			request.progressReceiver)
	default:
		return s.callProgressiveProgress(ctx, request.procedure, request.progressSender, request.progressReceiver)
	}
}

func (s *Session) waitForCallResult(ctx context.Context, channel chan *callResponse) CallResponse {
	select {
	case response := <-channel:
		if response.error != nil {
			return CallResponse{Err: response.error}
		}

		r := CallResponse{
			args:    NewList(response.msg.Args()),
			kwargs:  NewDict(response.msg.KwArgs()),
			details: NewDict(response.msg.Details()),
		}
		return r
	case <-ctx.Done():
		return CallResponse{Err: fmt.Errorf("call request timed out")}
	case <-s.leaveChan:
		return CallResponse{Err: fmt.Errorf("connection closed unexpectedly")}
	}
}

func (s *Session) Subscribe(topic string, handler EventHandler) *SubscribeRequest {
	return &SubscribeRequest{session: s, topic: topic, handler: handler}
}

func (s *Session) subscribe(topic string, handler EventHandler, options map[string]any) SubscribeResponse {
	subscribe := messages.NewSubscribe(s.idGen.NextID(), options, topic)
	if !s.Connected() {
		return SubscribeResponse{Err: fmt.Errorf("cannot subscribe to topic: session not established")}
	}

	toSend, err := s.serializer.Serialize(subscribe)
	if err != nil {
		return SubscribeResponse{Err: err}
	}

	channel := make(chan *subscribeResponse, 1)
	s.subscribeRequests.Store(subscribe.RequestID(), channel)
	defer s.subscribeRequests.Delete(subscribe.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return SubscribeResponse{Err: err}
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return SubscribeResponse{Err: response.error}
		}

		sub := &Subscription{
			id:           response.msg.SubscriptionID(),
			session:      s,
			eventHandler: handler,
		}

		subscriptions, exists := s.subscriptions.Load(response.msg.SubscriptionID())
		if !exists {
			s.subscriptions.Store(response.msg.SubscriptionID(), map[*Subscription]*Subscription{sub: sub})
		} else {
			subs := subscriptions.(map[*Subscription]*Subscription)
			subs[sub] = sub
			s.subscriptions.Store(response.msg.SubscriptionID(), subs)
		}
		return SubscribeResponse{subscription: sub}
	case <-time.After(10 * time.Second):
		return SubscribeResponse{Err: fmt.Errorf("subscribe request timed")}
	}
}

func (s *Session) publish(topic string, args []any, kwArgs map[string]any,
	options map[string]any) PublishResponse {
	if !s.Connected() {
		return PublishResponse{Err: fmt.Errorf("cannot publish to topic: session not established")}
	}

	publish := messages.NewPublish(s.idGen.NextID(), options, topic, args, kwArgs)
	toSend, err := s.serializer.Serialize(publish)
	if err != nil {
		return PublishResponse{Err: err}
	}

	ack, exists := publish.Options()["acknowledge"].(bool)
	if !exists || !ack {
		if err = s.base.Write(toSend); err != nil {
			return PublishResponse{Err: err}
		}

		return PublishResponse{}
	}

	channel := make(chan *publishResponse, 1)
	s.publishRequests.Store(publish.RequestID(), channel)
	defer s.publishRequests.Delete(publish.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return PublishResponse{Err: err}
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return PublishResponse{Err: response.error}
		}

		return PublishResponse{}
	case <-time.After(10 * time.Second):
		return PublishResponse{Err: fmt.Errorf("publish request timed")}
	}
}

func (s *Session) Publish(topic string) *PublishRequest {
	return &PublishRequest{session: s, topic: topic}
}

func (s *Session) Leave() error {
	if !s.Connected() {
		return fmt.Errorf("cannot leave: session not established")
	}

	goodbye := messages.NewGoodBye(CloseCloseRealm, map[string]any{})
	toSend, err := s.serializer.Serialize(goodbye)
	if err != nil {
		return err
	}

	if err = s.base.Write(toSend); err != nil {
		return err
	}

	select {
	case <-s.goodbyeChan:
		return nil
	case <-time.After(time.Second * 10):
		return errors.New("leave timeout")
	}
}

func (s *Session) SetOnLeaveListener(listener func()) {
	s.Lock()
	defer s.Unlock()

	s.onLeave = listener
}

func (s *Session) Details() *SessionDetails {
	return s.details
}

func (s *Session) Done() <-chan struct{} {
	return s.leaveChan
}

func (s *Session) GoodBye() *GoodBye {
	s.Lock()
	defer s.Unlock()

	return s.goodBye
}

func (s *Session) markDisconnected() {
	s.Lock()
	s.connected = false
	s.Unlock()

	s.leaveChan <- struct{}{}
}
