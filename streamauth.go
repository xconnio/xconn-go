package xconn

import (
	"context"
	"sync"
	"time"
)

// rawStreamRejected is the stream error code a raw stream is reset with when its
// connection has no authenticated WAMP session.
const rawStreamRejected = 1

// rawStreamAuthTimeout bounds how long a raw stream waits for its connection to
// authenticate a WAMP session before it is rejected.
var rawStreamAuthTimeout = 5 * time.Second //nolint:gochecknoglobals // overridden in tests

// connAuth records the first WAMP session authenticated on a QUIC/WebTransport
// connection; raw streams on that connection are only delivered after it.
type connAuth struct {
	once    sync.Once
	done    chan struct{}
	session BaseSession
}

func newConnAuth() *connAuth {
	return &connAuth{done: make(chan struct{})}
}

func (a *connAuth) authenticated(session BaseSession) {
	a.once.Do(func() {
		a.session = session
		close(a.done)
	})
}

// wait returns the connection's authenticated session, allowing rawStreamAuthTimeout
// for a client that opened the raw stream right after WELCOME.
func (a *connAuth) wait(ctx context.Context) (BaseSession, bool) {
	select {
	case <-a.done:
		return a.session, true
	case <-ctx.Done():
	case <-time.After(rawStreamAuthTimeout):
	}
	return nil, false
}
