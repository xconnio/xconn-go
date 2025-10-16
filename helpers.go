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
