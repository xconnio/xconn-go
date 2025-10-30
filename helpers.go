package xconn

import (
	"fmt"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
)

func ReadMessage(peer Peer, serializer serializers.Serializer) (messages.Message, error) {
	payload, err := peer.Read()
	if err != nil {
		return nil, err
	}

	msg, err := serializer.Deserialize(payload)
	if err != nil {
		return nil, err
	}

	return msg, nil
}

func ReadHello(peer Peer, serializer serializers.Serializer) (*messages.Hello, error) {
	msg, err := ReadMessage(peer, serializer)
	if err != nil {
		return nil, err
	}

	if msg.Type() != messages.MessageTypeHello {
		return nil, fmt.Errorf("first message must be HELLO, but was %d", msg.Type())
	}

	hello := msg.(*messages.Hello)
	return hello, nil
}

func WriteMessage(peer Peer, message messages.Message, serializer serializers.Serializer) error {
	payload, err := serializer.Serialize(message)
	if err != nil {
		return fmt.Errorf("failed to serialize message: %w", err)
	}

	if err = peer.Write(payload); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	return nil
}

func messageNameByID(id uint64) string {
	switch id {
	case messages.MessageTypeHello:
		return messages.MessageNameHello
	case messages.MessageTypeWelcome:
		return messages.MessageNameWelcome
	case messages.MessageTypeAbort:
		return messages.MessageNameAbort
	case messages.MessageTypeChallenge:
		return messages.MessageNameChallenge
	case messages.MessageTypeAuthenticate:
		return messages.MessageNameAuthenticate
	case messages.MessageTypeGoodbye:
		return messages.MessageNameGoodbye
	case messages.MessageTypeError:
		return messages.MessageNameError
	case messages.MessageTypePublish:
		return messages.MessageNamePublish
	case messages.MessageTypePublished:
		return messages.MessageNamePublished
	case messages.MessageTypeSubscribe:
		return messages.MessageNameSubscribe
	case messages.MessageTypeSubscribed:
		return messages.MessageNameSubscribed
	case messages.MessageTypeUnsubscribe:
		return messages.MessageNameUnsubscribe
	case messages.MessageTypeUnsubscribed:
		return messages.MessageNameUnsubscribed
	case messages.MessageTypeEvent:
		return messages.MessageNameEvent
	case messages.MessageTypeCall:
		return messages.MessageNameCall
	case messages.MessageTypeCancel:
		return messages.MessageNameCancel
	case messages.MessageTypeResult:
		return messages.MessageNameResult
	case messages.MessageTypeRegister:
		return messages.MessageNameRegister
	case messages.MessageTypeRegistered:
		return messages.MessageNameRegistered
	case messages.MessageTypeUnregister:
		return messages.MessageNameUnregister
	case messages.MessageTypeUnregistered:
		return messages.MessageNameUnregistered
	case messages.MessageTypeInvocation:
		return messages.MessageNameInvocation
	case messages.MessageTypeInterrupt:
		return messages.MessageNameInterrupt
	case messages.MessageTypeYield:
		return messages.MessageNameYield
	default:
		return "UNKNOWN"
	}
}

func constructReceivedMsgLog(msg messages.Message) string {
	switch msg.Type() {
	case messages.MessageTypeCall:
		callMsg := msg.(*messages.Call)
		return fmt.Sprintf("Received %s for procedure %s with request_id=%v args=%v, kwargs%v, options=%v",
			messages.MessageNameCall, callMsg.Procedure(), callMsg.RequestID(), callMsg.Args(), callMsg.KwArgs(),
			callMsg.Options())
	case messages.MessageTypeYield:
		yieldMsg := msg.(*messages.Yield)
		return fmt.Sprintf("Received %s with request_id=%v args=%v, kwargs=%v, options=%v",
			messages.MessageNameYield, yieldMsg.RequestID(), yieldMsg.Args(), yieldMsg.KwArgs(), yieldMsg.Options())
	case messages.MessageTypeRegister:
		registerMsg := msg.(*messages.Register)
		return fmt.Sprintf("Received %s for procedure=%s with request_id=%v, options=%v",
			messages.MessageNameRegister, registerMsg.Procedure(), registerMsg.RequestID(), registerMsg.Options())
	case messages.MessageTypeUnregister:
		unregisterMsg := msg.(*messages.Unregister)
		return fmt.Sprintf("Received %s with request_id=%v for registration=%v",
			messages.MessageNameUnregister, unregisterMsg.RequestID(), unregisterMsg.RegistrationID())
	case messages.MessageTypePublish:
		publishMsg := msg.(*messages.Publish)
		return fmt.Sprintf("Received %s for topic %s with args=%v, kwargs=%v, options=%v",
			messages.MessageNamePublish, publishMsg.Topic(), publishMsg.Args(), publishMsg.KwArgs(), publishMsg.Options())
	case messages.MessageTypeSubscribe:
		subscribeMsg := msg.(*messages.Subscribe)
		return fmt.Sprintf("Received %s for topic=%s with request_id=%v, options=%v",
			messages.MessageNameSubscribe, subscribeMsg.Topic(), subscribeMsg.RequestID(), subscribeMsg.Options())
	case messages.MessageTypeUnsubscribe:
		unsubscribeMsg := msg.(*messages.Unsubscribe)
		return fmt.Sprintf("Received %s with request_id=%v for subscription=%v",
			messages.MessageNameUnsubscribe, unsubscribeMsg.RequestID(), unsubscribeMsg.SubscriptionID())
	case messages.MessageTypeError:
		errorMsg := msg.(*messages.Error)
		return fmt.Sprintf("Received %s for message %v with uri=%s, args=%v, kwargs%v, details=%v",
			messages.MessageNameError, errorMsg.MessageType(), errorMsg.URI(), errorMsg.Args(), errorMsg.KwArgs(),
			errorMsg.Details())
	case messages.MessageTypeGoodbye:
		goodbyeMsg := msg.(*messages.GoodBye)
		return fmt.Sprintf("Received %s with reason %s", messages.MessageNameGoodbye, goodbyeMsg.Reason())
	default:
		return fmt.Sprintf("Received %+v", msg.Marshal())
	}
}

func constructSendingMsgLog(msg messages.Message) string {
	switch msg.Type() {
	case messages.MessageTypeResult:
		result := msg.(*messages.Result)
		return fmt.Sprintf("Sending %s for with request_id=%v args=%v, kwargs%v, details=%v",
			messages.MessageNameResult, result.RequestID(), result.Args(), result.KwArgs(), result.Details())
	case messages.MessageTypeInvocation:
		invocation := msg.(*messages.Invocation)
		return fmt.Sprintf("Sending %s with request_id=%v registration_id=%v args=%v, kwargs=%v, details=%v",
			messages.MessageNameInvocation, invocation.RequestID(), invocation.RegistrationID(), invocation.Args(),
			invocation.KwArgs(), invocation.Details())
	case messages.MessageTypeRegistered:
		registered := msg.(*messages.Registered)
		return fmt.Sprintf("Sending %s with request_id=%v, regitration_id=%v",
			messages.MessageNameRegistered, registered.RequestID(), registered.RegistrationID())
	case messages.MessageTypeUnregistered:
		unregistered := msg.(*messages.Unregistered)
		return fmt.Sprintf("Sending %s with request_id=%v",
			messages.MessageNameUnregistered, unregistered.RequestID())
	case messages.MessageTypeEvent:
		event := msg.(*messages.Event)
		return fmt.Sprintf("Sending %s with publication_id=%v subcription_id=%v args=%v, kwargs=%v, details=%v",
			messages.MessageNameEvent, event.PublicationID(), event.SubscriptionID(), event.Args(),
			event.KwArgs(), event.Details())
	case messages.MessageTypePublished:
		published := msg.(*messages.Published)
		return fmt.Sprintf("Sending %s with request_id=%v, publication_id=%v",
			messages.MessageNamePublished, published.RequestID(), published.PublicationID())
	case messages.MessageTypeSubscribed:
		subscribed := msg.(*messages.Subscribed)
		return fmt.Sprintf("Sending %s with request_id=%v, subscription_id=%v",
			messages.MessageNameSubscribed, subscribed.RequestID(), subscribed.SubscriptionID())
	case messages.MessageTypeUnsubscribed:
		unsubscribed := msg.(*messages.Unsubscribed)
		return fmt.Sprintf("Sending %s with request_id=%v", messages.MessageNameUnsubscribed, unsubscribed.RequestID())
	case messages.MessageTypeError:
		errorMsg := msg.(*messages.Error)
		return fmt.Sprintf("Sending %s for message %v with uri=%s, args=%v, kwargs%v, details=%v",
			messages.MessageNameError, errorMsg.MessageType(), errorMsg.URI(), errorMsg.Args(), errorMsg.KwArgs(),
			errorMsg.Details())
	case messages.MessageTypeGoodbye:
		goodbyeMsg := msg.(*messages.GoodBye)
		return fmt.Sprintf("Sending %s with reason %s", messages.MessageNameGoodbye, goodbyeMsg.Reason())
	case messages.MessageTypeAbort:
		abort := msg.(*messages.Abort)
		return fmt.Sprintf("Sending %s with reason %s, args=%v, kwargs%v, details=%v", messages.MessageNameAbort,
			abort.Reason(), abort.Args(), abort.KwArgs(), abort.Details())
	default:
		return fmt.Sprintf("Sending %+v", msg.Marshal())
	}
}
