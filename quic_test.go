package xconn_test

import (
	"context"
	"crypto/tls"
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

func setupQUICServer(t *testing.T) *xconn.Listener {
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
