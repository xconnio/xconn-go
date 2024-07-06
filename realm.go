package xconn

import (
	"fmt"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/xconn-go/internal"
)

type Realm struct {
	broker  *wampproto.Broker
	dealer  *wampproto.Dealer
	clients internal.Map[int64, BaseSession]
}

func NewRealm() *Realm {
	return &Realm{
		broker:  wampproto.NewBroker(),
		dealer:  wampproto.NewDealer(),
		clients: internal.Map[int64, BaseSession]{},
	}
}

func (r *Realm) AttachClient(base BaseSession) error {
	r.clients.Store(base.ID(), base)

	details := wampproto.NewSessionDetails(base.ID(), base.Realm(), base.AuthID(), base.AuthRole(),
		base.Serializer().Static())

	if err := r.broker.AddSession(details); err != nil {
		return err
	}

	if err := r.dealer.AddSession(details); err != nil {
		return err
	}

	return nil
}

func (r *Realm) DetachClient(base BaseSession) error {
	r.clients.Delete(base.ID())

	if err := r.broker.RemoveSession(base.ID()); err != nil {
		return err
	}

	if err := r.dealer.RemoveSession(base.ID()); err != nil {
		return err
	}

	return nil
}

func (r *Realm) ReceiveMessage(sessionID int64, msg messages.Message) error {
	switch msg.Type() {
	case messages.MessageTypeCall, messages.MessageTypeYield, messages.MessageTypeRegister,
		messages.MessageTypeUnregister:
		msgWithRecipient, err := r.dealer.ReceiveMessage(sessionID, msg)
		if err != nil {
			return err
		}

		client, _ := r.clients.Load(msgWithRecipient.Recipient)
		return client.WriteMessage(msgWithRecipient.Message)
	case messages.MessageTypeSubscribe, messages.MessageTypeUnsubscribe:
		msgWithRecipient, err := r.broker.ReceiveMessage(sessionID, msg)
		if err != nil {
			return err
		}

		client, _ := r.clients.Load(msgWithRecipient.Recipient)
		return client.WriteMessage(msgWithRecipient.Message)
	case messages.MessageTypePublish:
		publish := msg.(*messages.Publish)
		publication, err := r.broker.ReceivePublish(sessionID, publish)
		if err != nil {
			return err
		}

		for _, recipientID := range publication.Recipients {
			client, _ := r.clients.Load(recipientID)
			_ = client.WriteMessage(publication.Event)
		}

		if publication.Ack != nil {
			client, _ := r.clients.Load(publication.Ack.Recipient)
			_ = client.WriteMessage(publication.Ack.Message)
		}

		return nil
	case messages.MessageTypeGoodbye:
		if err := r.dealer.RemoveSession(sessionID); err != nil {
			return err
		}

		client, exists := r.clients.LoadAndDelete(sessionID)
		if !exists {
			return fmt.Errorf("goodbye: client does not exist")
		}

		goodbye := messages.NewGoodBye("wamp.close.goodbye_and_out", nil)
		if err := client.WriteMessage(goodbye); err != nil {
			return err
		}

		_ = client.Close()
		return nil
	default:
		return fmt.Errorf("unknown message type: %v", msg.Type())
	}
}
