package xconn

import (
	"context"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/util"
)

const (
	MetaProcedureSessionKill = "wamp.session.kill"

	MetaTopicSessionJoin  = "wamp.session.on_join"
	MetaTopicSessionLeave = "wamp.session.on_leave"
)

type meta struct {
	router  *Router
	realm   string
	session *Session
}

func newMetAPI(realm string, router *Router) (*meta, error) {
	session, err := ConnectInMemory(router, realm)
	if err != nil {
		return nil, err
	}

	return &meta{
		realm:   realm,
		router:  router,
		session: session,
	}, nil
}

func (m *meta) start() error {
	for uri, handler := range map[string]InvocationHandler{
		MetaProcedureSessionKill: m.handleSessionKill,
	} {
		response := m.session.Register(uri, handler).Do()
		if response.Err != nil {
			return response.Err
		}
	}

	return nil
}

func (m *meta) onJoin(base BaseSession) {
	details := map[string]any{
		"session":      base.ID(),
		"authid":       base.AuthID(),
		"authrole":     base.AuthRole(),
		"authmethod":   "",
		"authprovider": "",
	}

	// FIXME: use a goroutine pool
	go func() {
		if m.session != nil {
			m.session.Publish(MetaTopicSessionJoin).Args(details).Do()
		}
	}()
}

func (m *meta) onLeave(base BaseSession) {
	// FIXME: use a goroutine pool
	go func() {
		if m.session != nil {
			m.session.Publish(MetaTopicSessionLeave).Args(base.ID(), base.AuthID(), base.AuthRole()).Do()
		}
	}()
}

func (m *meta) handleSessionKill(_ context.Context, invocation *Invocation) *Result {
	if len(invocation.Arguments) != 1 {
		return &Result{Err: "wamp.error.invalid_argument"}
	}

	sessionID, ok := util.AsUInt64(invocation.Arguments[0])
	if !ok {
		return &Result{Err: "wamp.error.invalid_argument"}
	}

	if sessionID == m.session.ID() {
		return &Result{Err: "wamp.error.invalid_session"}
	}

	rlm, ok := m.router.realms.Load(m.realm)
	if !ok {
		return &Result{Err: "wamp.error.not_found"}
	}

	client, ok := rlm.clients.Load(sessionID)
	if !ok {
		return &Result{Err: "wamp.error.not_found"}
	}

	goodbye := messages.NewGoodBye("wamp.error.abort", nil)
	if err := client.WriteMessage(goodbye); err != nil {
		return &Result{Err: "wamp.error.internal_error"}
	}

	_ = client.Close()

	return &Result{}
}
