package xconn_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

const (
	roleAnonymous = "anonymous"
	prefixMatch   = "prefix"
)

func startYamuxRouter(t *testing.T) (addr string) {
	t.Helper()

	router, err := xconn.NewRouter(nil)
	require.NoError(t, err)
	err = router.AddRealm(realmName, xconn.DefaultRealmConfig())
	require.NoError(t, err)
	err = router.AddRealmRole(realmName, xconn.RealmRole{
		Name: roleAnonymous,
		Permissions: []xconn.Permission{{
			URI:            "",
			MatchPolicy:    prefixMatch,
			AllowCall:      true,
			AllowRegister:  true,
			AllowPublish:   true,
			AllowSubscribe: true,
		}},
	})
	require.NoError(t, err)

	server := xconn.NewServer(router, nil, nil)

	listener, err := server.ListenAndServeYamux(xconn.NetworkTCP, "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	return listener.Addr().String()
}

func TestYamuxWAMPSession(t *testing.T) {
	addr := startYamuxRouter(t)

	sess, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	require.True(t, sess.Connected())

	regResp := sess.Register("io.xconn.yamux.echo",
		func(ctx context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult(inv.Args()...)
		}).Do()
	require.NoError(t, regResp.Err)

	callResp := sess.Call("io.xconn.yamux.echo").Args("hello", "yamux").Do()
	require.NoError(t, callResp.Err)
	require.Equal(t, "hello", callResp.ArgStringOr(0, ""))
	require.Equal(t, "yamux", callResp.ArgStringOr(1, ""))
}

func TestClientConnectYamux(t *testing.T) {
	addr := startYamuxRouter(t)

	client := xconn.Client{}
	sess, err := client.ConnectYamux(context.Background(), addr, realmName)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	require.True(t, sess.Connected())

	// Verify WAMP operations work through the created session.
	callResp := sess.Call("no.procedure").Do()
	require.Error(t, callResp.Err)
}

func TestDialYamuxWithJSONSerializer(t *testing.T) {
	addr := startYamuxRouter(t)

	sess, err := xconn.DialYamux(context.Background(), addr, realmName, &xconn.YamuxDialerConfig{
		SerializerSpec: xconn.JSONSerializerSpec,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	require.True(t, sess.Connected())

	regResp := sess.Register("io.xconn.yamux.json.echo",
		func(ctx context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult(inv.Args()...)
		}).Do()
	require.NoError(t, regResp.Err)

	callResp := sess.Call("io.xconn.yamux.json.echo").Args("json", "works").Do()
	require.NoError(t, callResp.Err)
	require.Equal(t, "json", callResp.ArgStringOr(0, ""))
}

func TestYamuxSessionClose(t *testing.T) {
	addr := startYamuxRouter(t)

	sess, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)

	require.True(t, sess.Connected())
	require.NoError(t, sess.Close())

	require.Eventually(t, func() bool {
		return !sess.Connected()
	}, time.Second, 10*time.Millisecond)
}

// TestYamuxMultipleClients verifies that several concurrent yamux clients can each
// hold independent WAMP sessions on the same server.
func TestYamuxMultipleClients(t *testing.T) {
	const n = 5
	addr := startYamuxRouter(t)

	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
			require.NoError(t, err)
			defer func() { _ = sess.Close() }()

			require.True(t, sess.Connected())
			callResp := sess.Call("no.procedure").Do()
			require.Error(t, callResp.Err)
		}()
	}
	wg.Wait()
}

func TestYamuxPublishSubscribe(t *testing.T) {
	addr := startYamuxRouter(t)

	subscriber, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscriber.Close() })

	publisher, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = publisher.Close() })

	received := make(chan string, 1)
	subResp := subscriber.Subscribe("io.xconn.yamux.topic", func(event *xconn.Event) {
		received <- event.ArgStringOr(0, "")
	}).Do()
	require.NoError(t, subResp.Err)

	pubResp := publisher.Publish("io.xconn.yamux.topic").Args("hello").Acknowledge(true).Do()
	require.NoError(t, pubResp.Err)

	select {
	case msg := <-received:
		require.Equal(t, "hello", msg)
	case <-time.After(2 * time.Second):
		t.Fatal("event not received")
	}
}
