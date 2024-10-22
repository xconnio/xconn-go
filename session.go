package xconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
)

const ErrNoResult = "io.xconn.no_result"

type InvocationHandler func(ctx context.Context, invocation *Invocation) *Result
type EventHandler func(event *Event)
type ProgressHandler func(result *Result)
type SendProgressive func(ctx context.Context) *Progress

type Session struct {
	base  BaseSession
	proto *wampproto.Session

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

	onLeave   func()
	leaveChan chan struct{}
}

func NewSession(base BaseSession, serializer serializers.Serializer) *Session {
	session := &Session{
		base:  base,
		proto: wampproto.NewSession(serializer),
		idGen: &wampproto.SessionScopeIDGenerator{},

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

			_ = s.base.Close()
			return
		}

		msg, err := s.proto.Receive(payload)
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

		requestChan := request.(chan *RegisterResponse)
		requestChan <- &RegisterResponse{msg: registered}
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
				progHandler := progressHandler.(ProgressHandler)
				progHandler(&Result{
					Arguments:   result.Args(),
					KwArguments: result.KwArgs(),
					Details:     result.Details(),
				})
			}
		} else {
			req := request.(chan *CallResponse)
			req <- &CallResponse{msg: result}
			s.progressHandlers.Delete(result.RequestID())
		}

	case messages.MessageTypeInvocation:
		invocation := msg.(*messages.Invocation)
		end, _ := s.registrations.Load(invocation.RegistrationID())
		endpoint := end.(InvocationHandler)

		inv := &Invocation{
			Arguments:   invocation.Args(),
			KwArguments: invocation.KwArgs(),
			Details:     invocation.Details(),
		}

		progress, _ := invocation.Details()[wampproto.OptionProgress].(bool)
		receiveProgress, _ := invocation.Details()[wampproto.OptionReceiveProgress].(bool)
		if receiveProgress {
			progressFunc := func(arguments []any, kwArguments map[string]any) error {
				yield := messages.NewYield(invocation.RequestID(), map[string]any{"progress": true}, arguments, kwArguments)
				payload, err := s.proto.SendMessage(yield)
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
				inv.SendProgress = progressFunc.(func(arguments []any, kwArguments map[string]any) error)
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
					int64(invocation.Type()), invocation.RequestID(), map[string]any{}, res.Err, res.Arguments, res.KwArguments,
				)
			} else {
				msgToSend = messages.NewYield(invocation.RequestID(), nil, res.Arguments, res.KwArguments)
			}

			payload, err := s.proto.SendMessage(msgToSend)
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

		req := request.(chan *SubscribeResponse)
		req <- &SubscribeResponse{msg: subscribed}
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

		req := request.(chan *PublishResponse)
		req <- &PublishResponse{msg: published}
	case messages.MessageTypeEvent:
		event := msg.(*messages.Event)
		handler, exists := s.subscriptions.Load(event.SubscriptionID())
		if !exists {
			return fmt.Errorf("received EVENT for unknown subscription")
		}

		evt := &Event{
			Arguments:   event.Args(),
			KwArguments: event.KwArgs(),
			Details:     event.Details(),
		}
		eventHandler := handler.(EventHandler)
		go eventHandler(evt)
	case messages.MessageTypeError:
		errorMsg := msg.(*messages.Error)
		switch errorMsg.MessageType() {
		case messages.MessageTypeCall:
			response, exists := s.callRequests.LoadAndDelete(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid call request")
			}

			err := &Error{URI: errorMsg.URI(), Arguments: errorMsg.Args(), KwArguments: errorMsg.KwArgs()}
			responseChan := response.(chan *CallResponse)
			responseChan <- &CallResponse{error: err}
			return nil
		case messages.MessageTypeRegister:
			request, exists := s.registerRequests.LoadAndDelete(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid register request")
			}

			err := &Error{URI: errorMsg.URI(), Arguments: errorMsg.Args(), KwArguments: errorMsg.KwArgs()}
			requestChan := request.(chan *RegisterResponse)
			requestChan <- &RegisterResponse{error: err}
			return nil
		case messages.MessageTypeUnregister:
			_, exists := s.unregisterRequests.LoadAndDelete(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid unregister request")
			}

			return nil
		case messages.MessageTypeSubscribe:
			_, exists := s.subscribeRequests.Load(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid subscribe request")
			}

			s.subscribeRequests.Delete(errorMsg.RequestID())
			return nil
		case messages.MessageTypeUnsubscribe:
			_, exists := s.unsubscribeRequests.Load(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid unsubscribe request")
			}

			s.unsubscribeRequests.Delete(errorMsg.RequestID())
			return nil
		case messages.MessageTypePublish:
			_, exists := s.publishRequests.Load(errorMsg.RequestID())
			if !exists {
				return fmt.Errorf("received ERROR for invalid publish request")
			}

			s.publishRequests.Delete(errorMsg.RequestID())
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
		s.leaveChan <- struct{}{}
	default:
		return fmt.Errorf("SESSION: received unexpected message %T", msg)
	}

	return nil
}

func (s *Session) ID() int64 {
	return s.base.ID()
}

func (s *Session) Register(procedure string, handler InvocationHandler,
	options map[string]any) (*Registration, error) {

	register := messages.NewRegister(s.idGen.NextID(), options, procedure)
	toSend, err := s.proto.SendMessage(register)
	if err != nil {
		return nil, err
	}

	channel := make(chan *RegisterResponse, 1)
	s.registerRequests.Store(register.RequestID(), channel)
	defer s.registerRequests.Delete(register.RequestID())

	if err = s.base.Write(toSend); err != nil {
		return nil, err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return nil, response.error
		}

		s.registrations.Store(response.msg.RegistrationID(), handler)
		registration := &Registration{
			ID: response.msg.RegistrationID(),
		}
		return registration, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("register request timed out")
	}
}

func (s *Session) Unregister(registrationID int64) error {
	unregister := messages.NewUnregister(s.idGen.NextID(), registrationID)
	toSend, err := s.proto.SendMessage(unregister)
	if err != nil {
		return err
	}

	channel := make(chan *UnregisterResponse, 1)
	s.unregisterRequests.Store(unregister.RequestID(), channel)
	defer s.unregisterRequests.Delete(unregister.RequestID())

	if err = s.base.Write(toSend); err != nil {
		return err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return response.error
		}

		s.registrations.Delete(registrationID)
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("unregister request timed")
	}
}

func (s *Session) call(ctx context.Context, call *messages.Call) (*Result, error) {
	toSend, err := s.proto.SendMessage(call)
	if err != nil {
		return nil, err
	}

	channel := make(chan *CallResponse, 1)
	s.callRequests.Store(call.RequestID(), channel)
	defer s.callRequests.Delete(call.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return nil, err
	}

	return waitForCallResult(ctx, channel)
}

func (s *Session) Call(ctx context.Context, procedure string, args []any, kwArgs map[string]any,
	options map[string]any) (*Result, error) {

	call := messages.NewCall(s.idGen.NextID(), options, procedure, args, kwArgs)
	return s.call(ctx, call)
}

func (s *Session) CallProgress(ctx context.Context, procedure string, args []any, kwArgs map[string]any,
	options map[string]any, progressHandler ProgressHandler) (*Result, error) {

	call := messages.NewCall(s.idGen.NextID(), options, procedure, args, kwArgs)
	if progressHandler == nil {
		progressHandler = func(result *Result) {}
	}
	s.progressHandlers.Store(call.RequestID(), progressHandler)
	call.Options()[wampproto.OptionReceiveProgress] = true

	return s.call(ctx, call)
}

func (s *Session) CallProgressive(ctx context.Context, procedure string,
	progressFunc SendProgressive) (*Result, error) {
	progress := progressFunc(ctx)
	if progress.Err != nil {
		return nil, progress.Err
	}
	call := messages.NewCall(s.idGen.NextID(), progress.Options, procedure, progress.Arguments, progress.KwArguments)

	toSend, err := s.proto.SendMessage(call)
	if err != nil {
		return nil, err
	}

	channel := make(chan *CallResponse, 1)
	s.callRequests.Store(call.RequestID(), channel)
	defer s.callRequests.Delete(call.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return nil, err
	}

	callInProgress, _ := progress.Options[wampproto.OptionProgress].(bool)
	go func() {
		for callInProgress {
			prog := progressFunc(ctx)
			if prog.Err != nil {
				// TODO: implement call canceling
				return
			}
			call := messages.NewCall(call.RequestID(), prog.Options, procedure, prog.Arguments, prog.KwArguments)
			toSend, err = s.proto.SendMessage(call)
			if err != nil {
				return
			}
			if err = s.base.Write(toSend); err != nil {
				return
			}

			callInProgress, _ = prog.Options[wampproto.OptionProgress].(bool)
		}
	}()

	return waitForCallResult(ctx, channel)
}

func (s *Session) CallProgressiveProgress(ctx context.Context, procedure string,
	progressFunc SendProgressive, progressHandler ProgressHandler) (*Result, error) {

	if progressHandler == nil {
		progressHandler = func(result *Result) {}
	}

	progress := progressFunc(ctx)
	if progress.Err != nil {
		return nil, progress.Err
	}
	call := messages.NewCall(s.idGen.NextID(), progress.Options, procedure, progress.Arguments, progress.KwArguments)
	s.progressHandlers.Store(call.RequestID(), progressHandler)
	call.Options()[wampproto.OptionReceiveProgress] = true

	toSend, err := s.proto.SendMessage(call)
	if err != nil {
		return nil, err
	}

	channel := make(chan *CallResponse, 1)
	s.callRequests.Store(call.RequestID(), channel)
	defer s.callRequests.Delete(call.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return nil, err
	}

	callInProgress, _ := progress.Options[wampproto.OptionProgress].(bool)
	go func() {
		for callInProgress {
			prog := progressFunc(ctx)
			if prog.Err != nil {
				// TODO: implement call canceling
				return
			}
			call := messages.NewCall(call.RequestID(), prog.Options, procedure, prog.Arguments, prog.KwArguments)
			toSend, err = s.proto.SendMessage(call)
			if err != nil {
				return
			}
			if err = s.base.Write(toSend); err != nil {
				return
			}

			callInProgress, _ = prog.Options[wampproto.OptionProgress].(bool)
		}
	}()

	return waitForCallResult(ctx, channel)
}

func waitForCallResult(ctx context.Context, channel chan *CallResponse) (*Result, error) {
	select {
	case response := <-channel:
		if response.error != nil {
			return nil, response.error
		}

		result := &Result{
			Arguments:   response.msg.Args(),
			KwArguments: response.msg.KwArgs(),
			Details:     response.msg.Details(),
		}
		return result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("call request timed out")
	}
}

func (s *Session) Subscribe(topic string, handler EventHandler, options map[string]any) (*Subscription, error) {
	subscribe := messages.NewSubscribe(s.idGen.NextID(), options, topic)
	toSend, err := s.proto.SendMessage(subscribe)
	if err != nil {
		return nil, err
	}

	channel := make(chan *SubscribeResponse, 1)
	s.subscribeRequests.Store(subscribe.RequestID(), channel)
	defer s.subscribeRequests.Delete(subscribe.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return nil, err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return nil, response.error
		}

		s.subscriptions.Store(response.msg.SubscriptionID(), handler)
		sub := &Subscription{
			ID: response.msg.SubscriptionID(),
		}
		return sub, nil
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("subscribe request timed")
	}
}

func (s *Session) Unsubscribe(subscription *Subscription) error {
	unsubscribe := messages.NewUnsubscribe(s.idGen.NextID(), subscription.ID)
	toSend, err := s.proto.SendMessage(unsubscribe)
	if err != nil {
		return err
	}

	channel := make(chan *UnsubscribeResponse, 1)
	s.unsubscribeRequests.Store(unsubscribe.RequestID(), channel)
	defer s.unsubscribeRequests.Delete(unsubscribe.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return response.error
		}

		s.subscriptions.Delete(subscription.ID)
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("unsubscribe request timed")
	}
}

func (s *Session) Publish(topic string, args []any, kwArgs map[string]any,
	options map[string]any) error {

	publish := messages.NewPublish(s.idGen.NextID(), options, topic, args, kwArgs)
	toSend, err := s.proto.SendMessage(publish)
	if err != nil {
		return err
	}

	ack, exists := publish.Options()["acknowledge"].(bool)
	if !exists || !ack {
		if err = s.base.Write(toSend); err != nil {
			return err
		}

		return nil
	}

	channel := make(chan *PublishResponse, 1)
	s.publishRequests.Store(publish.RequestID(), channel)
	defer s.publishRequests.Delete(publish.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return response.error
		}

		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("publish request timed")
	}
}

func (s *Session) Leave() error {
	goodbye := messages.NewGoodBye(CloseCloseRealm, map[string]any{})
	toSend, err := s.proto.SendMessage(goodbye)
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
	s.onLeave = listener
}

func (s *Session) LeaveChan() <-chan struct{} {
	return s.leaveChan
}

func (s *Session) GoodBye() *GoodBye {
	return s.goodBye
}
