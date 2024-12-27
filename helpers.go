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
