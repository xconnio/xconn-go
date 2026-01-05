package xconn

import (
	"fmt"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/xconn-go/internal"
)

const ManagementRealm = "io.xconn.mgmt"

type LogLevel log.Level

const (
	LogLevelTrace = LogLevel(log.TraceLevel)
	LogLevelDebug = LogLevel(log.DebugLevel)
	LogLevelInfo  = LogLevel(log.InfoLevel)
	LogLevelWarn  = LogLevel(log.WarnLevel)
	LogLevelError = LogLevel(log.ErrorLevel)
)

type RouterConfig struct {
	Management bool
	LogLevel   LogLevel
}

func DefaultRouterConfig() *RouterConfig {
	return &RouterConfig{
		LogLevel: LogLevel(log.DebugLevel),
	}
}

type RealmConfig struct {
	AutoDiscloseCaller    bool
	AutoDisclosePublisher bool
	Meta                  bool
	Authorizer            Authorizer
	Roles                 []RealmRole
}

func DefaultRealmConfig() *RealmConfig {
	return &RealmConfig{}
}

type Router struct {
	realms internal.Map[string, *Realm]

	metaAPI internal.Map[string, *meta]

	managementAPI bool

	trackingMsg atomic.Bool
	msgCount    atomic.Uint64
	msgsPerSec  atomic.Uint64
	stopTrackCh chan struct{}
}

func NewRouter(cfg *RouterConfig) (*Router, error) {
	r := &Router{
		realms:      internal.Map[string, *Realm]{},
		stopTrackCh: make(chan struct{}),
	}

	if cfg == nil {
		return r, nil
	}

	if cfg.Management {
		if err := r.AddRealm(ManagementRealm, &RealmConfig{
			Roles: []RealmRole{{
				Name: "anonymous",
				Permissions: []Permission{
					{
						URI:            "io.xconn.mgmt.",
						MatchPolicy:    "prefix",
						AllowCall:      true,
						AllowSubscribe: true,
					},
				},
			}},
		}); err != nil {
			return nil, err
		}

		if err := r.enableManagementAPI(); err != nil {
			return nil, err
		}
	}

	if cfg.LogLevel == 0 {
		cfg.LogLevel = LogLevelInfo
	}

	log.SetLevel(log.Level(cfg.LogLevel))

	return r, nil
}

func (r *Router) AddRealm(name string, cfg *RealmConfig) error {
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

	if cfg == nil {
		r.realms.Store(name, realm)
		return nil
	}

	if cfg.AutoDiscloseCaller {
		realm.AutoDiscloseCaller(true)
	}

	if cfg.AutoDisclosePublisher {
		realm.AutoDisclosePublisher(true)
	}

	if cfg.Meta {
		if err := r.EnableMetaAPI(name); err != nil {
			return err
		}
	}

	if cfg.Authorizer != nil {
		if err := r.SetRealmAuthorizer(name, cfg.Authorizer); err != nil {
			return err
		}
	}

	for _, role := range cfg.Roles {
		if err := realm.AddRole(role); err != nil {
			return err
		}
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

	if err := realm.DetachClient(base); err != nil {
		return err
	}

	defer base.Close()

	if metaObj, ok := r.metaAPI.Load(base.Realm()); ok {
		metaObj.onLeave(base)
	}

	return nil
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

	if err := realm.ReceiveMessage(base, msg); err != nil {
		return err
	}

	if r.trackingMsg.Load() {
		r.msgCount.Add(1)
	}
	return nil
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
	return r.autoDiscloseCaller(realm, true)
}

func (r *Router) enableManagementAPI() error {
	if r.managementAPI {
		fmt.Println("management API is already enabled")
		return nil
	}

	session, err := ConnectInMemory(r, ManagementRealm)
	if err != nil {
		return err
	}

	managementAPI := newManagementAPI(session, r)

	if err = managementAPI.start(); err != nil {
		return err
	}

	r.managementAPI = true
	return r.autoDiscloseCaller(ManagementRealm, true)
}

func (r *Router) setMessageRateTracking(enabled bool) {
	if enabled {
		if r.trackingMsg.Load() {
			return // already running
		}
		r.trackingMsg.Store(true)
		ticker := time.NewTicker(time.Second)
		go func() {
			for {
				select {
				case <-ticker.C:
					count := r.msgCount.Swap(0)
					r.msgsPerSec.Store(count)
					log.Tracef("currently handling %d messages/sec", count)
				case <-r.stopTrackCh:
					ticker.Stop()
					return
				}
			}
		}()
	} else {
		select {
		case r.stopTrackCh <- struct{}{}:
		default:
		}
		r.trackingMsg.Store(false)
	}
}

func (r *Router) autoDiscloseCaller(realm string, disclose bool) error {
	realmObj, ok := r.realms.Load(realm)
	if !ok {
		return fmt.Errorf("could not find realm: %s", realm)
	}

	realmObj.AutoDiscloseCaller(disclose)
	return nil
}

func (r *Router) Close() {
	r.realms.Range(func(name string, realm *Realm) bool {
		realm.Close()
		r.realms.Delete(name)
		return true
	})
}
