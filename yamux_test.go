package xconn_test

import (
	"context"
	"io"
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

func startYamuxRouter(t *testing.T) (addr string, listener *xconn.YamuxListener) {
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

	listener, err = server.ListenAndServeYamux(xconn.NetworkTCP, "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	return listener.Addr().String(), listener
}

func TestYamuxWAMPSession(t *testing.T) {
	addr, _ := startYamuxRouter(t)

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
	addr, _ := startYamuxRouter(t)

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
	addr, _ := startYamuxRouter(t)

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
	addr, _ := startYamuxRouter(t)

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
	addr, _ := startYamuxRouter(t)

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
	addr, _ := startYamuxRouter(t)

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

func TestYamuxRawStream(t *testing.T) {
	addr, listener := startYamuxRouter(t)

	streamData := make(chan []byte, 1)
	go func() {
		stream := <-listener.AcceptStream()
		buf, err := io.ReadAll(stream)
		if err == nil {
			streamData <- buf
		}
		_ = stream.Close()
	}()

	sess, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	stream, err := sess.OpenStream()
	require.NoError(t, err)

	_, err = stream.Write([]byte("raw file data"))
	require.NoError(t, err)
	_ = stream.Close()

	received := <-streamData
	require.Equal(t, "raw file data", string(received))
}

func TestYamuxConnHandler(t *testing.T) {
	addr, listener := startYamuxRouter(t)

	// Server opens a stream to the client and writes a greeting.
	go func() {
		event := <-listener.Conns()
		stream, err := event.Conn.OpenStream()
		if err != nil {
			return
		}
		_, _ = stream.Write([]byte("hello from server"))
		_ = stream.Close()
	}()

	sess, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	stream, err := sess.AcceptStream()
	require.NoError(t, err)
	defer stream.Close()

	buf := make([]byte, 32)
	n, err := stream.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "hello from server", string(buf[:n]))
}

func TestYamuxStreamHasAuthenticatedSession(t *testing.T) {
	addr, listener := startYamuxRouter(t)

	sessionIDCh := make(chan uint64, 1)
	go func() {
		stream := <-listener.AcceptStream()
		sessionIDCh <- stream.Session.ID()
		_ = stream.Close()
	}()

	sess, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	stream, err := sess.OpenStream()
	require.NoError(t, err)
	_ = stream.Close()

	serverSessionID := <-sessionIDCh
	require.NotZero(t, serverSessionID)
}

func TestYamuxConnHandlerContextCancelledOnClose(t *testing.T) {
	addr, listener := startYamuxRouter(t)

	ctxDone := make(chan struct{})
	go func() {
		event := <-listener.Conns()
		<-event.Ctx.Done()
		close(ctxDone)
	}()

	sess, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)

	_ = sess.Close()

	select {
	case <-ctxDone:
	case <-time.After(2 * time.Second):
		t.Fatal("YamuxConnEvent.Ctx was not cancelled after session close")
	}
}

func TestYamuxMultipleConcurrentStreams(t *testing.T) {
	const n = 5
	addr, listener := startYamuxRouter(t)

	for range n {
		go func() {
			stream := <-listener.AcceptStream()
			defer stream.Close()
			buf := make([]byte, 1)
			if _, err := io.ReadFull(stream, buf); err != nil {
				return
			}
			_, _ = stream.Write(buf)
		}()
	}

	sess, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stream, err := sess.OpenStream()
			require.NoError(t, err)
			defer stream.Close()

			sent := []byte{byte(i)}
			_, err = stream.Write(sent)
			require.NoError(t, err)

			// Read the echo and verify it matches what we sent.
			got := make([]byte, 1)
			_, err = io.ReadFull(stream, got)
			require.NoError(t, err)
			require.Equal(t, sent, got)
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("not all concurrent streams were handled")
	}
}

func TestYamuxBidirectionalStream(t *testing.T) {
	addr, listener := startYamuxRouter(t)

	go func() {
		stream := <-listener.AcceptStream()
		defer stream.Close()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(stream, buf); err != nil {
			return
		}
		_, _ = stream.Write(buf)
	}()

	sess, err := xconn.DialYamux(context.Background(), addr, realmName, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })

	stream, err := sess.OpenStream()
	require.NoError(t, err)
	defer stream.Close()

	_, err = stream.Write([]byte("hello"))
	require.NoError(t, err)

	buf := make([]byte, 5)
	_, err = io.ReadFull(stream, buf)
	require.NoError(t, err)
	require.Equal(t, "hello", string(buf))
}
