package xconn

import (
	"fmt"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/util"
	"github.com/xconnio/xconn-go/internal"
)

type Realm struct {
	broker  *wampproto.Broker
	dealer  *wampproto.Dealer
	clients internal.Map[uint64, BaseSession]
	roles   internal.Map[string, RealmRole]
}

func NewRealm() *Realm {
	return &Realm{
		broker:  wampproto.NewBroker(),
		dealer:  wampproto.NewDealer(),
		clients: internal.Map[uint64, BaseSession]{},
		roles:   internal.Map[string, RealmRole]{},
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

func (r *Realm) AddRole(role RealmRole) error {
	_, loaded := r.roles.LoadOrStore(role.Name, role)
	if loaded {
		return fmt.Errorf("role '%s' already exists", role.Name)
	}

	return nil
}

func (r *Realm) RemoveRole(role string) error {
	_, exists := r.roles.Load(role)
	if !exists {
		return fmt.Errorf("role %s does not exists", role)
	}

	r.roles.Delete(role)
	return nil
}

func (r *Realm) HasRole(role string) bool {
	_, exists := r.roles.Load(role)
	return exists
}

func (r *Realm) isAuthorized(roleName string, msgType int, uri string) bool {
	role, ok := r.roles.Load(roleName)
	if !ok {
		return false
	}

	for _, perm := range role.Permissions {
		if !perm.Allows(msgType) {
			continue
		}

		if perm.MatchURI(uri) {
			return true
		}
	}

	return false
}

func (r *Realm) authorize(baseSession BaseSession, msgType int, uri string, requestID uint64) error {
	if !r.isAuthorized(baseSession.AuthRole(), msgType, uri) {
		messageType, _ := util.AsUInt64(msgType)
		errMsg := messages.NewError(messageType, requestID, map[string]any{},
			wampproto.ErrAuthorizationFailed, nil, nil)

		return baseSession.WriteMessage(errMsg)
	}

	return nil
}

func (r *Realm) handleDealerBoundMessage(baseSession BaseSession, msg messages.Message) error {
	msgWithRecipient, err := r.dealer.ReceiveMessage(baseSession.ID(), msg)
	if err != nil {
		return err
	}

	client, _ := r.clients.Load(msgWithRecipient.Recipient)
	return client.WriteMessage(msgWithRecipient.Message)
}

func (r *Realm) handleBrokerBoundMessage(baseSession BaseSession, msg messages.Message) error {
	msgWithRecipient, err := r.broker.ReceiveMessage(baseSession.ID(), msg)
	if err != nil {
		return err
	}

	client, _ := r.clients.Load(msgWithRecipient.Recipient)
	return client.WriteMessage(msgWithRecipient.Message)
}

func (r *Realm) ReceiveMessage(baseSession BaseSession, msg messages.Message) error {
	switch msg.Type() {
	case messages.MessageTypeCall:
		call := msg.(*messages.Call)
		if err := r.authorize(baseSession, call.Type(), call.Procedure(), call.RequestID()); err != nil {
			return err
		}

		return r.handleDealerBoundMessage(baseSession, msg)
	case messages.MessageTypeRegister:
		reg := msg.(*messages.Register)
		if err := r.authorize(baseSession, reg.Type(), reg.Procedure(), reg.RequestID()); err != nil {
			return err
		}

		return r.handleDealerBoundMessage(baseSession, msg)
	case messages.MessageTypeYield, messages.MessageTypeUnregister, messages.MessageTypeError:
		return r.handleDealerBoundMessage(baseSession, msg)
	case messages.MessageTypeSubscribe:
		sub := msg.(*messages.Subscribe)
		if err := r.authorize(baseSession, sub.Type(), sub.Topic(), sub.RequestID()); err != nil {
			return err
		}

		return r.handleBrokerBoundMessage(baseSession, msg)
	case messages.MessageTypeUnsubscribe:
		return r.handleBrokerBoundMessage(baseSession, msg)
	case messages.MessageTypePublish:
		publish := msg.(*messages.Publish)
		publication, err := r.broker.ReceivePublish(baseSession.ID(), publish)
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
		if err := r.dealer.RemoveSession(baseSession.ID()); err != nil {
			return err
		}

		client, exists := r.clients.LoadAndDelete(baseSession.ID())
		if !exists {
			return fmt.Errorf("goodbye: client does not exist")
		}

		goodbye := messages.NewGoodBye(CloseGoodByeAndOut, nil)
		if err := client.WriteMessage(goodbye); err != nil {
			return err
		}

		_ = client.Close()
		return nil
	default:
		return fmt.Errorf("unknown message type: %v", msg.Type())
	}
}

func (r *Realm) Close() {
	goodbye := messages.NewGoodBye(CloseSystemShutdown, nil)

	r.clients.Range(func(id uint64, client BaseSession) bool {
		_ = client.WriteMessage(goodbye)

		_ = client.Close()

		_ = r.DetachClient(client)

		return true
	})
}
