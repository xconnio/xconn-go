package xconn_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func setupWebTransportServer(t *testing.T) (*xconn.WebTransportListener, string) {
	t.Helper()

	router := initRouterWithRealm1(t)
	server := xconn.NewServer(router, nil, nil)

	tlsConfig, _, err := xconn.GenerateWebTransportTLSConfig()
	require.NoError(t, err)

	listener, err := server.ListenAndServeWebTransport("127.0.0.1:0", tlsConfig, "/wamp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	return listener, fmt.Sprintf("https://%s/wamp", listener.Addr().String())
}

func connectWebTransport(t *testing.T, url string) *xconn.WebTransportSession {
	t.Helper()
	sess, err := xconn.ConnectWebTransport(context.Background(), url, "realm1",
		&xconn.WebTransportDialerConfig{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestWebTransportJoin(t *testing.T) {
	_, url := setupWebTransportServer(t)
	sess := connectWebTransport(t, url)

	require.Equal(t, "realm1", sess.Details().Realm())
	require.Equal(t, "anonymous", sess.Details().AuthRole())
}

func TestWebTransportUniqueSessionIDs(t *testing.T) {
	_, url := setupWebTransportServer(t)
	s1 := connectWebTransport(t, url)
	s2 := connectWebTransport(t, url)
	require.NotEqual(t, s1.Details().ID(), s2.Details().ID())
}

func TestWebTransportRegisterCall(t *testing.T) {
	_, url := setupWebTransportServer(t)

	callee := connectWebTransport(t, url)
	caller := connectWebTransport(t, url)

	regResp := callee.Register("io.xconn.test.wt.add",
		func(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult("pong")
		}).Do()
	require.NoError(t, regResp.Err)

	callResp := caller.Call("io.xconn.test.wt.add").Do()
	require.NoError(t, callResp.Err)
	result, err := callResp.ArgString(0)
	require.NoError(t, err)
	require.Equal(t, "pong", result)

	require.NoError(t, regResp.Unregister())
	callResp = caller.Call("io.xconn.test.wt.add").Do()
	require.EqualError(t, callResp.Err, "wamp.error.no_such_procedure")
}

func TestWebTransportPublishSubscribe(t *testing.T) {
	_, url := setupWebTransportServer(t)

	subscriber := connectWebTransport(t, url)
	publisher := connectWebTransport(t, url)

	eventCh := make(chan *xconn.Event, 1)
	subResp := subscriber.Subscribe("io.xconn.test.wt.events", func(e *xconn.Event) {
		eventCh <- e
	}).Do()
	require.NoError(t, subResp.Err)

	pubResp := publisher.Publish("io.xconn.test.wt.events").ExcludeMe(false).Do()
	require.NoError(t, pubResp.Err)

	select {
	case ev := <-eventCh:
		require.NotNil(t, ev)
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive published event within 2s")
	}

	require.NoError(t, subResp.Unsubscribe())

	pubResp = publisher.Publish("io.xconn.test.wt.events").ExcludeMe(false).Do()
	require.NoError(t, pubResp.Err)

	select {
	case <-eventCh:
		t.Fatal("received event after unsubscribe")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWebTransportMultipleClients(t *testing.T) {
	const n = 5
	_, url := setupWebTransportServer(t)

	for i := range n {
		t.Run(fmt.Sprintf("client%d", i), func(t *testing.T) {
			t.Parallel()
			sess := connectWebTransport(t, url)
			require.Equal(t, "realm1", sess.Details().Realm())
		})
	}
}

func TestWebTransportSessionClose(t *testing.T) {
	_, url := setupWebTransportServer(t)
	sess := connectWebTransport(t, url)

	require.True(t, sess.Connected())
	require.NoError(t, sess.Close())

	require.Eventually(t, func() bool {
		return !sess.Connected()
	}, 200*time.Millisecond, 5*time.Millisecond)
}

// TestWebTransportMultiplexedSessions verifies that multiple independent WAMP sessions
// can share a single WebTransport connection via OpenSession.
func TestWebTransportMultiplexedSessions(t *testing.T) {
	const n = 3
	_, url := setupWebTransportServer(t)

	cfg := &xconn.WebTransportDialerConfig{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}

	first := connectWebTransport(t, url)

	sessions := []*xconn.WebTransportSession{first}
	for range n - 1 {
		sess, err := first.OpenSession(context.Background(), "realm1", cfg)
		require.NoError(t, err)
		t.Cleanup(func() { _ = sess.Close() })
		sessions = append(sessions, sess)
	}

	// All sessions must have unique IDs and the same realm.
	ids := make(map[uint64]bool)
	for _, s := range sessions {
		require.Equal(t, "realm1", s.Details().Realm())
		ids[s.Details().ID()] = true
	}
	require.Len(t, ids, n, "session IDs must be unique across multiplexed sessions")

	// Each session must be independently usable: register a procedure on one,
	// call it from another on the same connection.
	procedure := "io.xconn.test.wt.mux"
	regResp := sessions[0].Register(procedure,
		func(_ context.Context, _ *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult("mux-ok")
		}).Do()
	require.NoError(t, regResp.Err)

	for _, caller := range sessions[1:] {
		resp := caller.Call(procedure).Do()
		require.NoError(t, resp.Err)
		result, err := resp.ArgString(0)
		require.NoError(t, err)
		require.Equal(t, "mux-ok", result)
	}
}

func TestWebTransportRawStream(t *testing.T) {
	listener, url := setupWebTransportServer(t)

	streamData := make(chan []byte, 1)
	go func() {
		stream := <-listener.AcceptStream()
		buf, err := io.ReadAll(stream.Conn)
		if err == nil {
			streamData <- buf
		}
		_ = stream.Close()
	}()

	sess := connectWebTransport(t, url)

	stream, err := sess.OpenStream()
	require.NoError(t, err)

	_, err = stream.Write([]byte("raw data over webtransport"))
	require.NoError(t, err)
	_ = stream.Close()

	select {
	case received := <-streamData:
		require.Equal(t, "raw data over webtransport", string(received))
	case <-time.After(2 * time.Second):
		t.Fatal("raw stream data not received within 2s")
	}
}

func TestWebTransportConnHandlerContextCancelledOnClose(t *testing.T) {
	listener, url := setupWebTransportServer(t)

	ctxDone := make(chan struct{})
	go func() {
		event := <-listener.AcceptSession()
		<-event.Ctx.Done()
		close(ctxDone)
	}()

	sess := connectWebTransport(t, url)
	_ = sess.Close()

	select {
	case <-ctxDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("WebTransportPeerSession.Ctx was not cancelled after session close")
	}
}
