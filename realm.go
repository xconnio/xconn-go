package xconn

import (
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/util"
	"github.com/xconnio/xconn-go/internal"
)

type Realm struct {
	broker     *wampproto.Broker
	dealer     *wampproto.Dealer
	clients    internal.Map[uint64, BaseSession]
	roles      internal.Map[string, RealmRole]
	authorizer Authorizer
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
		base.Serializer().Static(), wampproto.RouterRoles)

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

func (r *Realm) AutoDiscloseCaller(disclose bool) {
	r.dealer.AutoDiscloseCaller(disclose)
}

func (r *Realm) AutoDisclosePublisher(disclose bool) {
	r.broker.AutoDisclosePublisher(disclose)
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

func (r *Realm) SetAuthorizer(authorizer Authorizer) {
	r.authorizer = authorizer
}

func (r *Realm) isAuthorized(roleName string, msgType uint64, uri string) bool {
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

func (r *Realm) authorize(baseSession BaseSession, msg messages.Message, uri string, requestID uint64,
	sendMsg bool) (bool, error) {
	if r.isAuthorized(baseSession.AuthRole(), msg.Type(), uri) {
		return true, nil
	}

	var authErr error
	if r.authorizer != nil {
		ok, err := r.authorizer.Authorize(baseSession, msg)
		if ok {
			return true, nil
		}
		authErr = err
	}

	if !sendMsg {
		return false, nil
	}

	var args []any
	if authErr != nil {
		args = []any{authErr.Error()}
	}
	errMsg := messages.NewError(msg.Type(), requestID, map[string]any{},
		wampproto.ErrAuthorizationFailed, args, nil)

	success, err := baseSession.TryWriteMessage(errMsg)
	if err != nil {
		return false, err
	}

	if !success {
		log.Debugf("dropped ERROR message (authz) for blocked peer: %d", baseSession.ID())
	}

	return false, nil
}

func (r *Realm) handleDealerBoundMessage(baseSession BaseSession, msg messages.Message) error {
	msgWithRecipient, err := r.dealer.ReceiveMessage(baseSession.ID(), msg)
	if err != nil {
		return err
	}

	client, exists := r.clients.Load(msgWithRecipient.Recipient)
	if !exists {
		return fmt.Errorf("could not find client for recipient: %d", msgWithRecipient.Recipient)
	}

	success, err := client.TryWriteMessage(msgWithRecipient.Message)
	if err != nil {
		return err
	}

	if !success {
		messageName := messageNameByID(msgWithRecipient.Message.Type())
		log.Debugf("dropped %s message for blocked peer: %d", messageName, baseSession.ID())
		if msg.Type() == messages.MessageTypeCall {
			caller, exists := r.clients.Load(baseSession.ID())
			if exists {
				callMsg := msg.(*messages.Call)
				errMsg := messages.NewError(msg.Type(), callMsg.RequestID(), nil,
					ErrNetworkFailure, []any{"callee blocked, cannot call procedure"}, nil)
				_, _ = caller.TryWriteMessage(errMsg)
			}
		}
	}

	return nil
}

func (r *Realm) handleBrokerBoundMessage(baseSession BaseSession, msg messages.Message) error {
	msgWithRecipient, err := r.broker.ReceiveMessage(baseSession.ID(), msg)
	if err != nil {
		return err
	}

	client, exists := r.clients.Load(msgWithRecipient.Recipient)
	if !exists {
		return fmt.Errorf("could not find client for recipient: %d", msgWithRecipient.Recipient)
	}

	success, err := client.TryWriteMessage(msgWithRecipient.Message)
	if err != nil {
		return err
	}

	if !success {
		messageName := messageNameByID(msgWithRecipient.Message.Type())
		log.Debugf("dropped %s message for blocked peer: %d", messageName, baseSession.ID())
	}

	return nil
}

func (r *Realm) ReceiveMessage(baseSession BaseSession, msg messages.Message) error {
	switch msg.Type() {
	case messages.MessageTypeCall:
		call := msg.(*messages.Call)
		authorized, err := r.authorize(baseSession, msg, call.Procedure(), call.RequestID(), true)
		if err != nil || !authorized {
			return err
		}

		return r.handleDealerBoundMessage(baseSession, msg)
	case messages.MessageTypeRegister:
		reg := msg.(*messages.Register)
		authorized, err := r.authorize(baseSession, msg, reg.Procedure(), reg.RequestID(), true)
		if err != nil || !authorized {
			return err
		}

		return r.handleDealerBoundMessage(baseSession, msg)
	case messages.MessageTypeYield, messages.MessageTypeUnregister, messages.MessageTypeError:
		return r.handleDealerBoundMessage(baseSession, msg)
	case messages.MessageTypeSubscribe:
		sub := msg.(*messages.Subscribe)
		authorized, err := r.authorize(baseSession, msg, sub.Topic(), sub.RequestID(), true)
		if err != nil || !authorized {
			return err
		}

		return r.handleBrokerBoundMessage(baseSession, msg)
	case messages.MessageTypeUnsubscribe:
		return r.handleBrokerBoundMessage(baseSession, msg)
	case messages.MessageTypePublish:
		publish := msg.(*messages.Publish)
		acknowledge := util.ToBool(msg.(*messages.Publish).Options()["acknowledge"])
		authorized, err := r.authorize(baseSession, msg, publish.Topic(), publish.RequestID(), acknowledge)
		if err != nil || !authorized {
			return err
		}

		publication, err := r.broker.ReceivePublish(baseSession.ID(), publish)
		if err != nil {
			return err
		}

		for _, recipientID := range publication.Recipients {
			client, ok := r.clients.Load(recipientID)
			if ok {
				success, err := client.TryWriteMessage(publication.Event)
				if err != nil {
					// likely the client disconnected, lets not bother here.
					continue
				}

				if !success {
					log.Debugf("dropped EVENT message for blocked peer: %d", recipientID)
				}
			}
		}

		if publication.Ack != nil {
			client, ok := r.clients.Load(publication.Ack.Recipient)
			if ok {
				success, err := client.TryWriteMessage(publication.Ack.Message)
				if err != nil {
					// likely the client disconnected, lets not bother here.
					return nil // nolint nilerr
				}

				if !success {
					log.Debugf("dropped PUBLISHED message for blocked peer: %d", publication.Ack.Recipient)
				}
			}
		}

		return nil
	case messages.MessageTypeGoodbye:
		goodbye := messages.NewGoodBye(CloseGoodByeAndOut, nil)
		success, err := baseSession.TryWriteMessage(goodbye)
		if err != nil {
			return err
		}

		if !success {
			log.Debugf("dropped GOODBYE message for blocked peer: %d", baseSession.ID())
		}

		_ = baseSession.Close()
		return nil
	default:
		return fmt.Errorf("unknown message type: %v", msg.Type())
	}
}

func (r *Realm) Close() {
	goodbye := messages.NewGoodBye(CloseSystemShutdown, nil)

	r.clients.Range(func(id uint64, client BaseSession) bool {
		_, _ = client.TryWriteMessage(goodbye)

		_ = client.Close()

		_ = r.DetachClient(client)

		return true
	})
}
