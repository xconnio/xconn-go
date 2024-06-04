package xconn

import (
	"fmt"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/xconn-go/internal"
)

type Router struct {
	realms internal.Map[string, *Realm]
}

func NewRouter() *Router {
	return &Router{
		realms: internal.Map[string, *Realm]{},
	}
}

func (r *Router) AddRealm(name string) {
	r.realms.Store(name, NewRealm())
}

func (r *Router) RemoveRealm(name string) {
	r.realms.Delete(name)
}

func (r *Router) HasRealm(name string) bool {
	_, exists := r.realms.Load(name)
	return exists
}

func (r *Router) AttachClient(base BaseSession) error {
	realm, ok := r.realms.Load(base.Realm())
	if !ok {
		return fmt.Errorf("xconn: could not find realm: %s", base.Realm())
	}

	return realm.AttachClient(base)
}

func (r *Router) DetachClient(base BaseSession) error {
	realm, ok := r.realms.Load(base.Realm())
	if !ok {
		return fmt.Errorf("xconn: could not find realm: %s", base.Realm())
	}

	return realm.DetachClient(base)
}

func (r *Router) ReceiveMessage(base BaseSession, msg messages.Message) error {
	realm, ok := r.realms.Load(base.Realm())
	if !ok {
		return fmt.Errorf("xconn: could not find realm: %s", base.Realm())
	}

	return realm.ReceiveMessage(base.ID(), msg)
}
