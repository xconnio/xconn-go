package xconn

import (
	"fmt"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/xconn-go/internal"
)

type Realm struct {
	dealer  *wampproto.Dealer
	clients internal.Map[int64, BaseSession]
}

func NewRealm() *Realm {
	return &Realm{
		dealer:  wampproto.NewDealer(),
		clients: internal.Map[int64, BaseSession]{},
	}
}

func (r *Realm) AttachClient(base BaseSession) error {
	r.clients.Store(base.ID(), base)

	details := wampproto.NewSessionDetails(base.ID(), base.Realm(), base.AuthID(), base.AuthRole(),
		base.Serializer().Static())
	return r.dealer.AddSession(details)
}

func (r *Realm) DetachClient(base BaseSession) error {
	r.clients.Delete(base.ID())
	return r.dealer.RemoveSession(base.ID())
}

func (r *Realm) ReceiveMessage(sessionID int64, msg messages.Message) error {
	switch msg.Type() {
	case messages.MessageTypeCall, messages.MessageTypeYield, messages.MessageTypeRegister,
		messages.MessageTypeUnRegister:
		msgWithRecipient, err := r.dealer.ReceiveMessage(sessionID, msg)
		if err != nil {
			return err
		}

		client, _ := r.clients.Load(msgWithRecipient.Recipient)
		return client.WriteMessage(msgWithRecipient.Message)
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
