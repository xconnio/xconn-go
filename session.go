package xconn

import (
	"context"
	"fmt"
	"log"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
)

type InvocationHandler func(ctx context.Context, invocation *Invocation) (*Result, error)
type EventHandler func(event *Event)

type Session struct {
	base  BaseSession
	proto *wampproto.Session

	// wamp id generator
	idGen *wampproto.SessionScopeIDGenerator

	// remote procedure calls data structures
	registerRequests   map[int64]chan *RegisterResponse
	unregisterRequests map[int64]chan *UnRegisterResponse
	registrations      map[int64]InvocationHandler
	callRequests       map[int64]chan *CallResponse

	// publish subscribe data structures
	subscribeRequests   map[int64]chan *SubscribeResponse
	unsubscribeRequests map[int64]chan *UnSubscribeResponse
	subscriptions       map[int64]EventHandler
	publishRequests     map[int64]chan *PublishResponse
}

func NewSession(base BaseSession, serializer serializers.Serializer) *Session {
	session := &Session{
		base:  base,
		proto: wampproto.NewSession(serializer),
		idGen: &wampproto.SessionScopeIDGenerator{},

		registerRequests:   map[int64]chan *RegisterResponse{},
		unregisterRequests: map[int64]chan *UnRegisterResponse{},
		registrations:      map[int64]InvocationHandler{},
		callRequests:       map[int64]chan *CallResponse{},

		subscribeRequests:   map[int64]chan *SubscribeResponse{},
		unsubscribeRequests: map[int64]chan *UnSubscribeResponse{},
		subscriptions:       map[int64]EventHandler{},
		publishRequests:     map[int64]chan *PublishResponse{},
	}

	go session.waitForRouterMessages()
	return session
}

func (s *Session) waitForRouterMessages() {
	for {
		payload, err := s.base.Read()
		if err != nil {
			log.Println("failed to read message: ", err)
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
		request, exists := s.registerRequests[registered.RequestID()]
		if !exists {
			return fmt.Errorf("received REGISTERED for unknown request")
		}

		request <- &RegisterResponse{msg: registered}
	case messages.MessageTypeUnRegistered:
		unregistered := msg.(*messages.UnRegistered)
		request, exists := s.unregisterRequests[unregistered.RequestID()]
		if !exists {
			return fmt.Errorf("received UNREGISTERED for unknown request")
		}

		request <- &UnRegisterResponse{msg: unregistered}
	case messages.MessageTypeResult:
		result := msg.(*messages.Result)
		request, exists := s.callRequests[result.RequestID()]
		if !exists {
			return fmt.Errorf("received RESULT for unknown request")
		}

		request <- &CallResponse{msg: result}
	case messages.MessageTypeInvocation:
		invocation := msg.(*messages.Invocation)
		endpoint := s.registrations[invocation.RegistrationID()]

		inv := &Invocation{
			Args:    invocation.Args(),
			KwArgs:  invocation.KwArgs(),
			Details: invocation.Details(),
		}
		res, err := endpoint(context.Background(), inv)
		if err != nil {
			return fmt.Errorf("error occurred while calling invocation handler: %w", err)
		}

		result := messages.NewYield(invocation.RequestID(), nil, res.Args, res.KwArgs)
		payload, err := s.proto.SendMessage(result)
		if err != nil {
			return fmt.Errorf("failed to send yield: %w", err)
		}

		if err = s.base.Write(payload); err != nil {
			return fmt.Errorf("failed to send yield: %w", err)
		}
	case messages.MessageTypeSubscribed:
		subscribed := msg.(*messages.Subscribed)
		request, exists := s.subscribeRequests[subscribed.RequestID()]
		if !exists {
			return fmt.Errorf("received SUBSCRIBED for unknown request")
		}

		request <- &SubscribeResponse{msg: subscribed}
	case messages.MessageTypeUnSubscribed:
		unsubscribed := msg.(*messages.UnSubscribed)
		request, exists := s.unsubscribeRequests[unsubscribed.RequestID()]
		if !exists {
			return fmt.Errorf("received UNSUBSCRIBED for unknown request")
		}

		request <- &UnSubscribeResponse{msg: unsubscribed}
	case messages.MessageTypePublished:
		published := msg.(*messages.Published)
		request, exists := s.publishRequests[published.RequestID()]
		if !exists {
			return fmt.Errorf("received PUBLISHED for unknown request")
		}

		request <- &PublishResponse{msg: published}
	case messages.MessageTypeEvent:
		event := msg.(*messages.Event)
		handler, exists := s.subscriptions[event.SubscriptionID()]
		if !exists {
			return fmt.Errorf("received PUBLISHED for unknown request")
		}

		evt := &Event{
			Args:    event.Args(),
			KwArgs:  event.KwArgs(),
			Details: event.Details(),
		}
		go handler(evt)
	case messages.MessageTypeError:
		errorMsg := msg.(*messages.Error)
		switch errorMsg.MessageType() {
		case messages.MessageTypeCall:
			responseChan, exists := s.callRequests[errorMsg.RequestID()]
			if !exists {
				return fmt.Errorf("received ERROR for invalid call request")
			}

			delete(s.callRequests, errorMsg.RequestID())
			err := &Error{URI: errorMsg.URI(), Args: errorMsg.Args(), KwArgs: errorMsg.KwArgs()}
			responseChan <- &CallResponse{error: err}
			return nil
		case messages.MessageTypeRegister:
			responseChan, exists := s.registerRequests[errorMsg.RequestID()]
			if !exists {
				return fmt.Errorf("received ERROR for invalid register request")
			}

			delete(s.registerRequests, errorMsg.RequestID())
			err := &Error{URI: errorMsg.URI(), Args: errorMsg.Args(), KwArgs: errorMsg.KwArgs()}
			responseChan <- &RegisterResponse{error: err}
			return nil
		case messages.MessageTypeUnRegister:
			_, exists := s.unregisterRequests[errorMsg.RequestID()]
			if !exists {
				return fmt.Errorf("received ERROR for invalid unregister request")
			}

			delete(s.unregisterRequests, errorMsg.RequestID())
			return nil
		case messages.MessageTypeSubscribe:
			_, exists := s.subscribeRequests[errorMsg.RequestID()]
			if !exists {
				return fmt.Errorf("received ERROR for invalid subscribe request")
			}

			delete(s.subscribeRequests, errorMsg.RequestID())
			return nil
		case messages.MessageTypeUnSubscribe:
			_, exists := s.unsubscribeRequests[errorMsg.RequestID()]
			if !exists {
				return fmt.Errorf("received ERROR for invalid unsubscribe request")
			}

			delete(s.unsubscribeRequests, errorMsg.RequestID())
			return nil
		case messages.MessageTypePublish:
			_, exists := s.publishRequests[errorMsg.RequestID()]
			if !exists {
				return fmt.Errorf("received ERROR for invalid publish request")
			}

			delete(s.publishRequests, errorMsg.RequestID())
			return nil
		default:
			return fmt.Errorf("unknown error message type %T", msg)
		}
	default:
		return fmt.Errorf("SESSION: received unexpected message %T", msg)
	}

	return nil
}

func (s *Session) Register(ctx context.Context, procedure string, handler InvocationHandler,
	options map[string]any) (*Registration, error) {

	register := messages.NewRegister(s.idGen.NextID(), options, procedure)
	toSend, err := s.proto.SendMessage(register)
	if err != nil {
		return nil, err
	}

	channel := make(chan *RegisterResponse, 1)
	s.registerRequests[register.RequestID()] = channel
	defer delete(s.registerRequests, register.RequestID())

	if err = s.base.Write(toSend); err != nil {
		return nil, err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return nil, response.error
		}

		s.registrations[response.msg.RegistrationID()] = handler
		registration := &Registration{
			ID: response.msg.RegistrationID(),
		}
		return registration, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("register request timed out")
	}
}

func (s *Session) UnRegister(ctx context.Context, registrationID int64) error {
	unregister := messages.NewUnRegister(s.idGen.NextID(), registrationID)
	toSend, err := s.proto.SendMessage(unregister)
	if err != nil {
		return err
	}

	channel := make(chan *UnRegisterResponse, 1)
	s.unregisterRequests[unregister.RequestID()] = channel
	defer delete(s.unregisterRequests, unregister.RequestID())

	if err = s.base.Write(toSend); err != nil {
		return err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return response.error
		}

		delete(s.registrations, registrationID)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("unregister request timed")
	}
}

func (s *Session) Call(ctx context.Context, procedure string, args []any, kwArgs map[string]any,
	options map[string]any) (*Result, error) {

	call := messages.NewCall(s.idGen.NextID(), options, procedure, args, kwArgs)
	toSend, err := s.proto.SendMessage(call)
	if err != nil {
		return nil, err
	}

	channel := make(chan *CallResponse, 1)
	s.callRequests[call.RequestID()] = channel
	defer delete(s.callRequests, call.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return nil, err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return nil, response.error
		}

		result := &Result{
			Args:    response.msg.Args(),
			KwArgs:  response.msg.KwArgs(),
			Details: response.msg.Details(),
		}
		return result, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("call request timed out")
	}
}

func (s *Session) Subscribe(ctx context.Context, topic string, handler EventHandler,
	options map[string]any) (*Subscription, error) {

	subscribe := messages.NewSubscribe(s.idGen.NextID(), options, topic)
	toSend, err := s.proto.SendMessage(subscribe)
	if err != nil {
		return nil, err
	}

	channel := make(chan *SubscribeResponse, 1)
	s.subscribeRequests[subscribe.RequestID()] = channel
	defer delete(s.subscribeRequests, subscribe.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return nil, err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return nil, response.error
		}

		s.subscriptions[subscribe.RequestID()] = handler
		sub := &Subscription{
			ID: response.msg.SubscriptionID(),
		}
		return sub, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("subscribe request timed")
	}
}

func (s *Session) UnSubscribe(ctx context.Context, subscription *Subscription) error {
	unsubscribe := messages.NewUnSubscribe(s.idGen.NextID(), subscription.ID)
	toSend, err := s.proto.SendMessage(unsubscribe)
	if err != nil {
		return err
	}

	channel := make(chan *UnSubscribeResponse, 1)
	s.unsubscribeRequests[unsubscribe.RequestID()] = channel
	defer delete(s.unsubscribeRequests, unsubscribe.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return response.error
		}

		delete(s.subscriptions, subscription.ID)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("subscribe request timed")
	}
}

func (s *Session) Publish(ctx context.Context, topic string, args []any, kwArgs map[string]any,
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
	s.publishRequests[publish.RequestID()] = channel
	defer delete(s.publishRequests, publish.RequestID())
	if err = s.base.Write(toSend); err != nil {
		return err
	}

	select {
	case response := <-channel:
		if response.error != nil {
			return response.error
		}

		return nil
	case <-ctx.Done():
		return fmt.Errorf("publish request timed")
	}
}
