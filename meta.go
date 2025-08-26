package xconn

import (
	"context"
	"fmt"

	"github.com/xconnio/wampproto-go/messages"
)

const (
	MetaProcedureSessionKill  = "wamp.session.kill"
	MetaProcedureSessionCount = "wamp.session.count"
	MetaProcedureSessionList  = "wamp.session.list"
	MetaProcedureSessionGet   = "wamp.session.get"

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
		MetaProcedureSessionList:  m.handleSessionList,
		MetaProcedureSessionGet:   m.handleSessionGet,
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

func killSession(invocation *Invocation, client BaseSession) error {
	reason := invocation.KwargStringOr("reason", "wamp.close.killed")
	goodByeDetails := map[string]any{}

	if msg, err := invocation.KwargString("message"); err == nil {
		goodByeDetails["message"] = msg
	}

	goodbye := messages.NewGoodBye(reason, goodByeDetails)
	if err := client.WriteMessage(goodbye); err != nil {
		return fmt.Errorf("wamp.error.internal_error")
	}

	_ = client.Close()
	return nil
}

func (m *meta) handleSessionKill(_ context.Context, invocation *Invocation) *InvocationResult {
	sessionID, err := invocation.ArgUInt64(0)
	if err != nil {
		return NewInvocationError("wamp.error.invalid_argument")
	}

	if sessionID == m.session.ID() || sessionID == invocation.Caller() {
		return NewInvocationError("wamp.error.no_such_session")
	}

	rlm, ok := m.router.realms.Load(m.realm)
	if !ok {
		return NewInvocationError("wamp.error.not_found")
	}

	client, ok := rlm.clients.Load(sessionID)
	if !ok {
		return NewInvocationError("wamp.error.not_found")
	}

	if err := killSession(invocation, client); err != nil {
		return NewInvocationError(err.Error())
	}

	return NewInvocationResult()
}

func (m *meta) forEachSession(invocation *Invocation, fn func(sess BaseSession)) error {
	var roles []any
	if len(invocation.Args()) > 0 {
		r, err := invocation.ArgList(0)
		if err != nil {
			return fmt.Errorf("wamp.error.invalid_argument")
		}
		roles = r
	}

	rlm, ok := m.router.realms.Load(m.realm)
	if !ok {
		return fmt.Errorf("wamp.error.not_found")
	}

	rlm.clients.Range(func(_ uint64, sess BaseSession) bool {
		if len(roles) == 0 || contains(roles, sess.AuthRole()) {
			fn(sess)
		}
		return true
	})

	return nil
}

func (m *meta) handleSessionCount(_ context.Context, invocation *Invocation) *InvocationResult {
	var count uint64
	err := m.forEachSession(invocation, func(sess BaseSession) {
		count++
	})
	if err != nil {
		return NewInvocationError(err.Error())
	}
	return NewInvocationResult(count)
}

func (m *meta) handleSessionList(_ context.Context, invocation *Invocation) *InvocationResult {
	var sessionIDs []uint64
	err := m.forEachSession(invocation, func(sess BaseSession) {
		sessionIDs = append(sessionIDs, sess.ID())
	})
	if err != nil {
		return NewInvocationError(err.Error())
	}
	return NewInvocationResult(sessionIDs)
}

func (m *meta) handleSessionGet(_ context.Context, invocation *Invocation) *InvocationResult {
	if invocation.ArgsLen() != 1 {
		return NewInvocationError("wamp.error.invalid_argument")
	}

	sessionID, err := invocation.ArgUInt64(0)
	if err != nil {
		return NewInvocationError("wamp.error.invalid_argument")
	}

	rlm, ok := m.router.realms.Load(m.realm)
	if !ok {
		return NewInvocationError("wamp.error.not_found")
	}

	client, ok := rlm.clients.Load(sessionID)
	if !ok {
		return NewInvocationError("wamp.error.no_such_session")
	}

	details := map[string]any{
		"session":      client.ID(),
		"authid":       client.AuthID(),
		"authrole":     client.AuthRole(),
		"authmethod":   "",
		"authprovider": "",
	}
	return NewInvocationResult(details)
}

func contains(slice []any, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}
