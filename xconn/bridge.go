package xconn

import (
	"context"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/xconn-go"
)

const metaTopicSessionLeave = "wamp.session.on_leave"

// callerSessions are the app-layer sessions one bridge forwards calls through: one per
// remote caller, so the app tells callers apart by inv.Caller() and sees each leave.
type callerSessions struct {
	app      *App
	mu       sync.Mutex
	sessions map[uint64]*xconn.Session
}

// get returns caller's app-layer session, opening it on its first call. Without
// app.NewSession, or a disclosed caller, every call goes through app.Session.
func (c *callerSessions) get(caller uint64) (*xconn.Session, error) {
	if c.app.NewSession == nil || caller == 0 {
		return c.app.Session, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if session, ok := c.sessions[caller]; ok {
		return session, nil
	}
	session, err := c.app.NewSession()
	if err != nil {
		return nil, err
	}
	c.sessions[caller] = session
	return session, nil
}

// leave closes the app-layer session of caller, which left.
func (c *callerSessions) leave(caller uint64) {
	c.mu.Lock()
	session, ok := c.sessions[caller]
	delete(c.sessions, caller)
	c.mu.Unlock()
	if ok {
		_ = session.Leave()
	}
}

// leaveAll closes every caller's app-layer session, once the bridge's session is gone.
func (c *callerSessions) leaveAll() {
	c.mu.Lock()
	sessions := c.sessions
	c.sessions = map[uint64]*xconn.Session{}
	c.mu.Unlock()
	for _, session := range sessions {
		_ = session.Leave()
	}
}

// bridgeHandler forwards an invocation into the same-named procedure on the caller's
// app-layer session, chaining progressive results through so streaming procedures still
// work end to end. With no procedure, it forwards to the procedure that was called, for
// prefixes.
func bridgeHandler(callers *callerSessions, procedure string) xconn.InvocationHandler {
	return func(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
		called := procedure
		if called == "" {
			called = inv.Procedure()
		}
		appSession, err := callers.get(inv.Caller())
		if err != nil {
			return xconn.NewInvocationError(errOperationFailed, err.Error())
		}
		call := appSession.Call(called).Args(inv.Args()...).Kwargs(inv.Kwargs())
		if receiveProgress, _ := inv.Details()[wampproto.OptionReceiveProgress].(bool); receiveProgress {
			call = call.ProgressReceiver(func(r *xconn.ProgressResult) {
				_ = inv.SendProgress(r.Args(), r.Kwargs())
			})
		}

		resp := call.Do()
		if resp.Err != nil {
			return xconn.NewInvocationError(errOperationFailed, resp.Err.Error())
		}
		return xconn.NewInvocationResult(resp.Args()...)
	}
}

// RegisterBridge registers a forwarding handler on session for each of app's procedures
// and prefixes, relaying every call into the app layer (see App.NewSession).
func RegisterBridge(session *xconn.Session, app *App) error {
	callers := &callerSessions{app: app, sessions: map[uint64]*xconn.Session{}}
	if app.NewSession != nil {
		// Without session meta events, callers' app-layer sessions stay open until
		// session is gone.
		resp := session.Subscribe(metaTopicSessionLeave, func(event *xconn.Event) {
			if caller, err := event.ArgUInt64(0); err == nil {
				callers.leave(caller)
			}
		}).Do()
		if resp.Err != nil {
			log.Printf("bridge: not following callers leaving: %v", resp.Err)
		}
		safeGo(func() {
			<-session.Done()
			callers.leaveAll()
		})
	}

	for _, procedure := range app.Procedures {
		resp := session.Register(procedure, bridgeHandler(callers, procedure)).Invoke(wampproto.InvokeLast).Do()
		if resp.Err != nil {
			return resp.Err
		}
	}
	for _, prefix := range app.Prefixes {
		resp := session.Register(prefix, bridgeHandler(callers, "")).
			Match(wampproto.MatchPrefix).Invoke(wampproto.InvokeLast).Do()
		if resp.Err != nil {
			return resp.Err
		}
	}
	return nil
}
