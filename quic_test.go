package xconn_test

import (
	"context"
	"crypto/tls"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

const (
	roleAnonymous = "anonymous"
	matchPrefix   = "prefix"
)

func setupQUICServer(t *testing.T) *xconn.QUICListener {
	router, err := xconn.NewRouter(nil)
	require.NoError(t, err)
	require.NoError(t, router.AddRealm(realmName, xconn.DefaultRealmConfig()))
	require.NoError(t, router.AddRealmRole(realmName, xconn.RealmRole{
		Name: roleAnonymous,
		Permissions: []xconn.Permission{{
			URI:            "",
			MatchPolicy:    matchPrefix,
			AllowCall:      true,
			AllowRegister:  true,
			AllowPublish:   true,
			AllowSubscribe: true,
		}},
	}))

	tlsConfig, err := xconn.GenerateSelfSignedTLSConfig()
	require.NoError(t, err)

	server := xconn.NewServer(router, nil, nil)
	listener, err := server.ListenAndServeQUIC("127.0.0.1:0", tlsConfig)
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	return listener
}

func dialQUIC(t *testing.T, address string, spec xconn.SerializerSpec) *xconn.QUICSession {
	clientTLSConfig := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{xconn.NextProtoWAMP}}

	session, err := xconn.DialQUIC(context.Background(), address, realmName, &xconn.QUICDialerConfig{
		TLSConfig:      clientTLSConfig,
		SerializerSpec: spec,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	return session
}

func TestQUICTransport(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			listener := setupQUICServer(t)
			session := dialQUIC(t, listener.Addr().String(), spec)

			registerResp := session.Register(testURI,
				func(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
					return xconn.NewInvocationResult("hello")
				}).Do()
			require.NoError(t, registerResp.Err)

			callResp := session.Call(testURI).Do()
			require.NoError(t, callResp.Err)

			result, err := callResp.ArgString(0)
			require.NoError(t, err)
			require.Equal(t, "hello", result)
		})
	})
}

func TestQUICRegisterCall(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			listener := setupQUICServer(t)
			callee := dialQUIC(t, listener.Addr().String(), spec)
			caller := dialQUIC(t, listener.Addr().String(), spec)

			regResp := callee.Register(testURI,
				func(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
					return xconn.NewInvocationResult("hello")
				}).Do()
			require.NoError(t, regResp.Err)

			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					callResp := caller.Call(testURI).Do()
					require.NoError(t, callResp.Err)
					require.Equal(t, "hello", callResp.ArgStringOr(0, ""))
				}()
			}
			wg.Wait()

			require.NoError(t, regResp.Unregister())

			callResp := caller.Call(testURI).Do()
			require.EqualError(t, callResp.Err, "wamp.error.no_such_procedure")
		})
	})
}

func TestQUICPublishSubscribe(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			listener := setupQUICServer(t)
			subscriber := dialQUIC(t, listener.Addr().String(), spec)
			publisher := dialQUIC(t, listener.Addr().String(), spec)

			eventCh := make(chan *xconn.Event, 16)
			subResp := subscriber.Subscribe(testURI, func(event *xconn.Event) {
				eventCh <- event
			}).Do()
			require.NoError(t, subResp.Err)

			for i := 0; i < 16; i++ {
				go func() {
					pubResp := publisher.Publish(testURI).ExcludeMe(false).Do()
					require.NoError(t, pubResp.Err)
				}()
			}

			for i := 0; i < 16; i++ {
				ev := <-eventCh
				require.NotNil(t, ev)
			}

			require.NoError(t, subResp.Unsubscribe())

			pubResp := publisher.Publish(testURI).Do()
			require.NoError(t, pubResp.Err)

			select {
			case <-eventCh:
				t.Fatal("received event after unsubscribe")
			case <-time.After(100 * time.Millisecond):
			}
		})
	})
}

func TestQUICSessionDetails(t *testing.T) {
	listener := setupQUICServer(t)
	session := dialQUIC(t, listener.Addr().String(), xconn.CBORSerializerSpec)

	require.Equal(t, realmName, session.Details().Realm())
}

func TestQUICMultiplexedStream(t *testing.T) {
	listener := setupQUICServer(t)
	session := dialQUIC(t, listener.Addr().String(), xconn.CBORSerializerSpec)

	conn := <-listener.Conns()
	require.NotNil(t, conn.Conn)

	serverStream, err := conn.Conn.OpenStream()
	require.NoError(t, err)

	message := []byte("hello over raw stream")
	go func() {
		_, _ = serverStream.Write(message)
	}()

	clientStream, err := session.AcceptStream()
	require.NoError(t, err)

	buf := make([]byte, len(message))
	_, err = clientStream.Read(buf)
	require.NoError(t, err)
	require.Equal(t, message, buf)
}

func TestQUICSessionClose(t *testing.T) {
	listener := setupQUICServer(t)
	session := dialQUIC(t, listener.Addr().String(), xconn.CBORSerializerSpec)

	require.True(t, session.Connected())
	require.NoError(t, session.Close())

	require.Eventually(t, func() bool {
		return !session.Connected()
	}, 200*time.Millisecond, 5*time.Millisecond)
}

func TestQUICMultipleClients(t *testing.T) {
	const n = 5
	listener := setupQUICServer(t)

	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess := dialQUIC(t, listener.Addr().String(), xconn.CBORSerializerSpec)
			require.True(t, sess.Connected())
			callResp := sess.Call("no.procedure").Do()
			require.Error(t, callResp.Err)
		}()
	}
	wg.Wait()
}

// TestQUICRawStream verifies the client-to-server direction: client calls OpenStream,
// server receives it via listener.AcceptStream().
func TestQUICRawStream(t *testing.T) {
	listener := setupQUICServer(t)

	streamData := make(chan []byte, 1)
	go func() {
		stream := <-listener.AcceptStream()
		buf, err := io.ReadAll(stream.Conn)
		if err == nil {
			streamData <- buf
		}
		_ = stream.Close()
	}()

	sess := dialQUIC(t, listener.Addr().String(), xconn.CBORSerializerSpec)

	stream, err := sess.OpenStream()
	require.NoError(t, err)

	_, err = stream.Write([]byte("raw file data"))
	require.NoError(t, err)
	_ = stream.Close()

	received := <-streamData
	require.Equal(t, "raw file data", string(received))
}

func TestQUICRawStreamDelivered(t *testing.T) {
	listener := setupQUICServer(t)

	received := make(chan struct{}, 1)
	go func() {
		stream := <-listener.AcceptStream()
		_ = stream.Close()
		close(received)
	}()

	sess := dialQUIC(t, listener.Addr().String(), xconn.CBORSerializerSpec)

	stream, err := sess.OpenStream()
	require.NoError(t, err)
	_ = stream.Close()

	select {
	case <-received:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("raw stream was not delivered to AcceptStream")
	}
}

func TestQUICConnHandlerContextCancelledOnClose(t *testing.T) {
	listener := setupQUICServer(t)

	ctxDone := make(chan struct{})
	go func() {
		event := <-listener.Conns()
		<-event.Ctx.Done()
		close(ctxDone)
	}()

	sess := dialQUIC(t, listener.Addr().String(), xconn.CBORSerializerSpec)
	_ = sess.Close()

	select {
	case <-ctxDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("QUICConn.Ctx was not cancelled after session close")
	}
}

func TestQUICMultipleConcurrentStreams(t *testing.T) {
	const n = 5
	listener := setupQUICServer(t)

	for range n {
		go func() {
			stream := <-listener.AcceptStream()
			defer stream.Close()
			buf := make([]byte, 1)
			if _, err := io.ReadFull(stream.Conn, buf); err != nil {
				return
			}
			_, _ = stream.Write(buf)
		}()
	}

	sess := dialQUIC(t, listener.Addr().String(), xconn.CBORSerializerSpec)

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
	case <-time.After(2 * time.Second):
		t.Fatal("not all concurrent streams were handled")
	}
}

func TestQUICBidirectionalStream(t *testing.T) {
	listener := setupQUICServer(t)

	go func() {
		stream := <-listener.AcceptStream()
		defer stream.Close()
		buf := make([]byte, 5)
		if _, err := io.ReadFull(stream.Conn, buf); err != nil {
			return
		}
		_, _ = stream.Write(buf)
	}()

	sess := dialQUIC(t, listener.Addr().String(), xconn.CBORSerializerSpec)

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
