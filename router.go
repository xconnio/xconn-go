package xconn

import (
	"fmt"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/xconn-go/internal"
)

const ManagementRealm = "io.xconn.mgmt"

type Router struct {
	realms internal.Map[string, *Realm]

	metaAPI internal.Map[string, *meta]

	managementAPI bool
}

func NewRouter() *Router {
	return &Router{
		realms: internal.Map[string, *Realm]{},
	}
}

func (r *Router) AddRealm(name string) error {
	_, ok := r.realms.Load(name)
	if ok {
		return fmt.Errorf("realm '%s' already registered", name)
	}

	realm := NewRealm()

	perms := []Permission{{
		URI:            "",
		MatchPolicy:    "prefix",
		AllowCall:      true,
		AllowRegister:  true,
		AllowPublish:   true,
		AllowSubscribe: true,
	}}

	if err := realm.AddRole(RealmRole{Name: "trusted", Permissions: perms}); err != nil {
		return err
	}

	r.realms.Store(name, realm)
	return nil
}

func (r *Router) AddRealmAlias(realm, alias string) error {
	rlm, ok := r.realms.Load(realm)
	if !ok {
		return fmt.Errorf("realm '%s' not found", realm)
	}

	_, ok = r.realms.Load(alias)
	if ok {
		return fmt.Errorf("realm '%s' already registered", alias)
	}

	r.realms.Store(alias, rlm)
	return nil
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
		return fmt.Errorf("could not find realm: %s", base.Realm())
	}

	if err := realm.AttachClient(base); err == nil {
		metaObj, ok := r.metaAPI.Load(base.Realm())
		if ok {
			metaObj.onJoin(base)
		}

		return nil
	} else {
		return err
	}
}

func (r *Router) DetachClient(base BaseSession) error {
	realm, ok := r.realms.Load(base.Realm())
	if !ok {
		return fmt.Errorf("could not find realm: %s", base.Realm())
	}

	if err := realm.DetachClient(base); err == nil {
		metaObj, ok := r.metaAPI.Load(base.Realm())
		if ok {
			metaObj.onLeave(base)
		}

		return nil
	} else {
		return err
	}
}

func (r *Router) AddRealmRole(realm string, role RealmRole) error {
	realmObj, ok := r.realms.Load(realm)
	if !ok {
		return fmt.Errorf("could not find realm: %s", realm)
	}

	return realmObj.AddRole(role)
}

func (r *Router) SetRealmAuthorizer(realm string, authorizer Authorizer) error {
	realmObj, ok := r.realms.Load(realm)
	if !ok {
		return fmt.Errorf("could not find realm: %s", realm)
	}

	realmObj.SetAuthorizer(authorizer)
	return nil
}

func (r *Router) HasRealmRole(realm string, roleName string) (bool, error) {
	realmObj, ok := r.realms.Load(realm)
	if !ok {
		return false, fmt.Errorf("could not find realm: %s", realm)
	}

	return realmObj.HasRole(roleName), nil
}

func (r *Router) RemoveRealmRole(realm string, roleName string) error {
	realmObj, ok := r.realms.Load(realm)
	if !ok {
		return fmt.Errorf("could not find realm: %s", realm)
	}

	return realmObj.RemoveRole(roleName)
}

func (r *Router) ReceiveMessage(base BaseSession, msg messages.Message) error {
	realm, ok := r.realms.Load(base.Realm())
	if !ok {
		return fmt.Errorf("could not find realm: %s", base.Realm())
	}

	return realm.ReceiveMessage(base, msg)
}

func (r *Router) EnableMetaAPI(realm string) error {
	metaAPI, err := newMetAPI(realm, r)
	if err != nil {
		return err
	}

	if err = metaAPI.start(); err != nil {
		return err
	}

	r.metaAPI.Store(realm, metaAPI)
	return r.AutoDiscloseCaller(realm, true)
}

func (r *Router) EnableManagementAPI() error {
	if r.managementAPI {
		fmt.Println("management API is already enabled")
		return nil
	}

	if err := r.AddRealm(ManagementRealm); err != nil {
		return err
	}

	session, err := ConnectInMemory(r, ManagementRealm)
	if err != nil {
		return err
	}

	managementAPI := newManagementAPI(session)

	if err = managementAPI.start(); err != nil {
		return err
	}

	r.managementAPI = true
	return nil
}

func (r *Router) AutoDiscloseCaller(realm string, disclose bool) error {
	realmObj, ok := r.realms.Load(realm)
	if !ok {
		return fmt.Errorf("could not find realm: %s", realm)
	}

	realmObj.AutoDiscloseCaller(disclose)
	return nil
}

func (r *Router) AutoDisclosePublisher(realm string, disclose bool) error {
	realmObj, ok := r.realms.Load(realm)
	if !ok {
		return fmt.Errorf("could not find realm: %s", realm)
	}

	realmObj.AutoDisclosePublisher(disclose)
	return nil
}

func (r *Router) Close() {
	r.realms.Range(func(name string, realm *Realm) bool {
		realm.Close()
		return true
	})
}
