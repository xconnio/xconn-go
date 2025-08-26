package xconn

import (
	"context"

	"github.com/xconnio/wampproto-go/messages"
)

const (
	MetaProcedureSessionKill  = "wamp.session.kill"
	MetaProcedureSessionCount = "wamp.session.count"

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
		MetaProcedureSessionKill:  m.handleSessionKill,
		MetaProcedureSessionCount: m.handleSessionCount,
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

func (m *meta) handleSessionKill(_ context.Context, invocation *Invocation) *InvocationResult {
	if invocation.ArgsLen() != 1 {
		return &InvocationResult{Err: "wamp.error.invalid_argument"}
	}

	sessionID, err := invocation.ArgUInt64(0)
	if err != nil {
		return &InvocationResult{Err: "wamp.error.invalid_argument"}
	}

	if sessionID == m.session.ID() {
		return &InvocationResult{Err: "wamp.error.invalid_session"}
	}

	rlm, ok := m.router.realms.Load(m.realm)
	if !ok {
		return &InvocationResult{Err: "wamp.error.not_found"}
	}

	client, ok := rlm.clients.Load(sessionID)
	if !ok {
		return &InvocationResult{Err: "wamp.error.not_found"}
	}

	goodbye := messages.NewGoodBye("wamp.error.abort", nil)
	if err := client.WriteMessage(goodbye); err != nil {
		return &InvocationResult{Err: "wamp.error.internal_error"}
	}

	_ = client.Close()

	return &InvocationResult{}
}

func (m *meta) handleSessionCount(_ context.Context, invocation *Invocation) *InvocationResult {
	var roles []any
	if len(invocation.Args()) > 0 {
		r, err := invocation.ArgList(0)
		if err != nil {
			return NewInvocationError("wamp.error.invalid_argument", err.Error())
		}
		roles = r
	}

	rlm, ok := m.router.realms.Load(m.realm)
	if !ok {
		return NewInvocationError("wamp.error.not_found")
	}

	var count uint64
	rlm.clients.Range(func(_ uint64, sess BaseSession) bool {
		if len(roles) == 0 || contains(roles, sess.AuthRole()) {
			count++
		}
		return true
	})

	return NewInvocationResult(count)
}

func contains(slice []any, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
