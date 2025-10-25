package xconn_test

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"math/rand"
	netURL "net/url"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
	"github.com/xconnio/wampproto-go/transports"
	"github.com/xconnio/xconn-go"
	"github.com/xconnio/xconn-go/auth"
)

const realmName = "test"

func TestRouterMetaKill(t *testing.T) {
	router := xconn.NewRouter()
	err := router.AddRealm(realmName)
	require.NoError(t, err)
	require.NoError(t, router.EnableMetaAPI(realmName))

	session1, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	joinChan := make(chan *xconn.Event)
	subResponse := session1.Subscribe(xconn.MetaTopicSessionJoin, func(event *xconn.Event) {
		joinChan <- event
	}).Do()
	require.NoError(t, subResponse.Err)

	leaveChan := make(chan *xconn.Event)
	subResponse = session1.Subscribe(xconn.MetaTopicSessionLeave, func(event *xconn.Event) {
		leaveChan <- event
	}).Do()
	require.NoError(t, subResponse.Err)

	session2, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		<-joinChan
		return true
	}, 1*time.Second, 50*time.Millisecond)

	response := session1.Call(xconn.MetaProcedureSessionKill).Args(session2.ID()).Do()
	require.NoError(t, response.Err)

	require.Eventually(t, func() bool {
		<-leaveChan
		return true
	}, 1*time.Second, 50*time.Millisecond)

	require.Eventually(t, func() bool {
		return !session2.Connected()
	}, 1*time.Second, 50*time.Millisecond)
}

func TestRouterMetaKillByAuthID(t *testing.T) {
	router := xconn.NewRouter()
	err := router.AddRealm(realmName)
	require.NoError(t, err)
	require.NoError(t, router.EnableMetaAPI(realmName))

	session, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	// Create sessions with authid "test"
	baseSession, err := xconn.ConnectInMemoryBase(router, realmName, "test", "trusted", &serializers.JSONSerializer{})
	require.NoError(t, err)
	session1 := xconn.NewSession(baseSession, baseSession.Serializer())

	baseSession1, err := xconn.ConnectInMemoryBase(router, realmName, "test", "trusted", &serializers.JSONSerializer{})
	require.NoError(t, err)
	session2 := xconn.NewSession(baseSession1, baseSession.Serializer())

	// Kill all sessions with authid "test"
	resp := session.Call(xconn.MetaProcedureSessionKillByAuthID).Arg(baseSession.AuthID()).Do()
	require.NoError(t, resp.Err)
	sessionList, err := resp.ArgList(0)
	require.NoError(t, err)
	require.Contains(t, sessionList, session1.ID())
	require.Contains(t, sessionList, session2.ID())

	// Verify both sessions are disconnected
	require.Eventually(t, func() bool {
		return !session1.Connected()
	}, 1*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		return !session2.Connected()
	}, 1*time.Second, 50*time.Millisecond)

	// Session with different authid should remain connected
	require.True(t, session.Connected())

	// Test error case
	resp = session.Call(xconn.MetaProcedureSessionKillByAuthID).Do()
	require.EqualError(t, resp.Err, "wamp.error.invalid_argument: index 0 out of range [0, 0]")
}

func TestRouterMetaKillByAuthRole(t *testing.T) {
	router := xconn.NewRouter()
	err := router.AddRealm(realmName)
	require.NoError(t, err)
	require.NoError(t, router.EnableMetaAPI(realmName))

	session, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	// Create sessions with authrole "test"
	baseSession, err := xconn.ConnectInMemoryBase(router, realmName, "test", "test", &serializers.JSONSerializer{})
	require.NoError(t, err)
	session1 := xconn.NewSession(baseSession, baseSession.Serializer())

	baseSession1, err := xconn.ConnectInMemoryBase(router, realmName, "test", "test", &serializers.JSONSerializer{})
	require.NoError(t, err)
	session2 := xconn.NewSession(baseSession1, baseSession.Serializer())

	// Kill all sessions with authrole "test"
	resp := session.Call(xconn.MetaProcedureSessionKillByAuthRole).Arg(baseSession.AuthRole()).Do()
	require.NoError(t, resp.Err)
	sessionList, err := resp.ArgList(0)
	require.NoError(t, err)
	require.Contains(t, sessionList, session1.ID())
	require.Contains(t, sessionList, session2.ID())

	// Verify both sessions are disconnected
	require.Eventually(t, func() bool {
		return !session1.Connected()
	}, 1*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		return !session2.Connected()
	}, 1*time.Second, 50*time.Millisecond)

	// Session with different authrole should remain connected
	require.True(t, session.Connected())

	// Test error case
	resp = session.Call(xconn.MetaProcedureSessionKillByAuthRole).Do()
	require.EqualError(t, resp.Err, "wamp.error.invalid_argument: index 0 out of range [0, 0]")
}

func TestRouterMetaKillAll(t *testing.T) {
	router := xconn.NewRouter()
	err := router.AddRealm(realmName)
	require.NoError(t, err)
	require.NoError(t, router.EnableMetaAPI(realmName))

	session, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	session1, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)
	session2, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	// Kill all sessions
	resp := session.Call(xconn.MetaProcedureSessionKillAll).Do()
	require.NoError(t, resp.Err)
	sessionList, err := resp.ArgList(0)
	require.NoError(t, err)
	require.Contains(t, sessionList, session1.ID())
	require.Contains(t, sessionList, session2.ID())

	// Verify both sessions are disconnected
	require.Eventually(t, func() bool {
		return !session1.Connected()
	}, 1*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		return !session2.Connected()
	}, 1*time.Second, 50*time.Millisecond)

	// Caller session should remain connected
	require.True(t, session.Connected())
}

func TestRouterMetaSessionCount(t *testing.T) {
	router := xconn.NewRouter()
	require.NoError(t, router.AddRealm(realmName))
	require.NoError(t, router.EnableMetaAPI(realmName))

	// Connect first session
	session, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	t.Run("CountSessionWithRoleTrusted", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionCount).Arg([]any{"trusted"}).Do()
		require.NoError(t, resp.Err)
		require.Equal(t, uint64(2), resp.ArgUInt64Or(0, 0))
	})

	// Connect second session with role=admin
	baseSession, err := xconn.ConnectInMemoryBase(router, realmName, "test", "admin", &serializers.JSONSerializer{})
	require.NoError(t, err)
	session2 := xconn.NewSession(baseSession, baseSession.Serializer())

	t.Run("CountAllSessions", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionCount).Do()
		require.NoError(t, resp.Err)
		require.Equal(t, uint64(3), resp.ArgUInt64Or(0, 0))
	})

	t.Run("CountOnlyAdminSessions", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionCount).Arg([]any{"admin"}).Do()
		require.NoError(t, resp.Err)
		require.Equal(t, uint64(1), resp.ArgUInt64Or(0, 0))
	})

	// Disconnect admin session
	require.NoError(t, session2.Leave())

	t.Run("CountAfterAdminSessionLeaves", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionCount).Do()
		require.NoError(t, resp.Err)
		require.Equal(t, uint64(2), resp.ArgUInt64Or(0, 0))
	})
}

func TestRouterMetaSessionList(t *testing.T) {
	router := xconn.NewRouter()
	require.NoError(t, router.AddRealm(realmName))
	require.NoError(t, router.EnableMetaAPI(realmName))

	// Connect first session
	session, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	t.Run("ListSessionsWithRoleTrusted", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionList).Arg([]any{"trusted"}).Do()
		require.NoError(t, resp.Err)
		ids := resp.ArgListOr(0, nil)
		require.Len(t, ids, 2)
	})

	// Connect second session with role=admin
	baseSession, err := xconn.ConnectInMemoryBase(router, realmName, "test", "admin", &serializers.JSONSerializer{})
	require.NoError(t, err)
	session2 := xconn.NewSession(baseSession, baseSession.Serializer())

	t.Run("ListAllSessions", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionList).Do()
		require.NoError(t, resp.Err)
		ids := resp.ArgListOr(0, nil)
		require.Len(t, ids, 3)
	})

	t.Run("ListOnlyAdminSessions", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionList).Arg([]any{"admin"}).Do()
		require.NoError(t, resp.Err)
		ids := resp.ArgListOr(0, nil)
		require.Len(t, ids, 1)
		require.Equal(t, session2.ID(), ids[0])
	})

	// Disconnect admin session
	require.NoError(t, session2.Leave())

	t.Run("ListAfterAdminSessionLeaves", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionList).Do()
		require.NoError(t, resp.Err)
		ids := resp.ArgListOr(0, nil)
		require.Len(t, ids, 2)
	})
}

func TestRouterMetaSessionGet(t *testing.T) {
	router := xconn.NewRouter()
	require.NoError(t, router.AddRealm(realmName))
	require.NoError(t, router.EnableMetaAPI(realmName))

	session, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	expectedDetails := map[string]any{"authid": session.Details().AuthID(), "authmethod": "", "authprovider": "",
		"authrole": "trusted", "session": session.ID()}
	resp := session.Call(xconn.MetaProcedureSessionGet).Arg(session.ID()).Do()
	require.NoError(t, resp.Err)
	require.Equal(t, expectedDetails, resp.Args()[0])

	// test err
	respErr := session.Call(xconn.MetaProcedureSessionGet).Arg(uint64(2152454520)).Do()
	require.Equal(t, "wamp.error.no_such_session: invalid session id", respErr.Err.Error())
}

func TestAuthorization(t *testing.T) {
	router := xconn.NewRouter()
	err := router.AddRealm(realmName)
	require.NoError(t, err)

	addRole := func(name string, permissions []xconn.Permission) {
		err := router.AddRealmRole(realmName, xconn.RealmRole{
			Name:        name,
			Permissions: permissions,
		})
		require.NoError(t, err)
	}

	createSession := func(role string) *xconn.Session {
		authID := fmt.Sprintf("%012x", rand.Uint64())[:12] // #nosec
		baseSession, err := xconn.ConnectInMemoryBase(router, realmName, authID, role, &serializers.JSONSerializer{})
		require.NoError(t, err)
		return xconn.NewSession(baseSession, baseSession.Serializer())
	}

	t.Run("AllowRegisterCall", func(t *testing.T) {
		addRole("registerCall", []xconn.Permission{{
			URI:           "io.xconn.test",
			MatchPolicy:   wampproto.MatchExact,
			AllowRegister: true,
			AllowCall:     true,
		}})
		session := createSession("registerCall")

		registerResp := session.Register("io.xconn.test",
			func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
				return &xconn.InvocationResult{}
			}).Do()
		require.NoError(t, registerResp.Err)

		callResp := session.Call("io.xconn.test").Do()
		require.NoError(t, callResp.Err)

		publishResp := session.Publish("io.xconn.test").Acknowledge(true).Do()
		require.EqualError(t, publishResp.Err, "wamp.error.authorization_failed")

		subscribeResp := session.Subscribe("io.xconn.test", func(event *xconn.Event) {}).Do()
		require.EqualError(t, subscribeResp.Err, "wamp.error.authorization_failed")
	})

	t.Run("AllowPublish", func(t *testing.T) {
		addRole("publishOnly", []xconn.Permission{{
			URI:          "io.xconn.",
			MatchPolicy:  wampproto.MatchPrefix,
			AllowPublish: true,
		}})
		session := createSession("publishOnly")

		callResp := session.Call("io.xconn.test").Do()
		require.EqualError(t, callResp.Err, "wamp.error.authorization_failed")

		publishResp := session.Publish("io.xconn.test").Do()
		require.NoError(t, publishResp.Err)

		subscribeResp := session.Subscribe("io.xconn.test", func(event *xconn.Event) {}).Do()
		require.EqualError(t, subscribeResp.Err, "wamp.error.authorization_failed")

		registerResp := session.Register("io.xconn.test",
			func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
				return &xconn.InvocationResult{}
			}).Do()
		require.EqualError(t, registerResp.Err, "wamp.error.authorization_failed")
	})

	t.Run("AllowSubscribeAndPublish", func(t *testing.T) {
		addRole("subscriberAndPublish", []xconn.Permission{{
			URI:            "io.*.test",
			MatchPolicy:    wampproto.MatchWildcard,
			AllowSubscribe: true,
			AllowPublish:   true,
		}})
		session := createSession("subscriberAndPublish")

		registerResp := session.Register("io.xconn.test",
			func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
				return &xconn.InvocationResult{}
			}).Do()
		require.EqualError(t, registerResp.Err, "wamp.error.authorization_failed")

		callResp := session.Call("io.xconn.test").Do()
		require.EqualError(t, callResp.Err, "wamp.error.authorization_failed")

		subscribeResp := session.Subscribe("io.xconn.test", func(event *xconn.Event) {}).Do()
		require.NoError(t, subscribeResp.Err)

		publishResp := session.Publish("io.xconn.test").Acknowledge(true).Do()
		require.NoError(t, publishResp.Err)
	})
}

func TestRealmAlias(t *testing.T) {
	r := xconn.NewRouter()
	err := r.AddRealm("hello")
	require.NoError(t, err)

	require.True(t, r.HasRealm("hello"))
	require.False(t, r.HasRealm("alias"))

	err = r.AddRealmAlias("hello", "alias")
	require.NoError(t, err)
	require.True(t, r.HasRealm("hello"))
	require.True(t, r.HasRealm("alias"))
}

func TestAutoDiscloseCallerAndPublisher(t *testing.T) {
	router := xconn.NewRouter()
	err := router.AddRealm(realmName)
	require.NoError(t, err)

	callee, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	caller, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	callDetailsCh := make(chan map[string]any, 3)
	callee.Register("io.xconn.test",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			callDetailsCh <- invocation.Details()
			return xconn.NewInvocationResult()
		}).Do()

	publishDetailsCh := make(chan map[string]any, 1)
	callee.Subscribe("io.xconn.test", func(event *xconn.Event) {
		publishDetailsCh <- event.Details()
	}).Do()

	t.Run("DisabledByDefault", func(t *testing.T) {
		caller.Call("io.xconn.test").Do()
		require.Equal(t, map[string]any{}, <-callDetailsCh)

		caller.Publish("io.xconn.test").Do()
		require.Equal(t, map[string]any{}, <-publishDetailsCh)
	})

	t.Run("Enable", func(t *testing.T) {
		expectedCallerDetails := map[string]any{"caller": caller.ID(),
			"caller_authid": caller.Details().AuthID(), "caller_authrole": "trusted", "procedure": "io.xconn.test"}
		err = router.AutoDiscloseCaller(realmName, true)
		require.NoError(t, err)
		caller.Call("io.xconn.test").Do()
		require.Equal(t, expectedCallerDetails, <-callDetailsCh)

		expectedPublisherDetails := map[string]any{"publisher": caller.ID(),
			"publisher_authid": caller.Details().AuthID(), "publisher_authrole": "trusted", "topic": "io.xconn.test"}
		err = router.AutoDisclosePublisher(realmName, true)
		require.NoError(t, err)
		caller.Publish("io.xconn.test").Do()
		require.Equal(t, expectedPublisherDetails, <-publishDetailsCh)
	})

	t.Run("Disable", func(t *testing.T) {
		err = router.AutoDiscloseCaller(realmName, false)
		require.NoError(t, err)
		caller.Call("io.xconn.test").Do()
		require.Equal(t, map[string]any{}, <-callDetailsCh)

		err = router.AutoDisclosePublisher(realmName, false)
		require.NoError(t, err)
		caller.Publish("io.xconn.test").Do()
		require.Equal(t, map[string]any{}, <-publishDetailsCh)
	})
}

type testAuthorizer struct{}

func (a *testAuthorizer) Authorize(baseSession xconn.BaseSession, msg messages.Message) (bool, error) {
	role := baseSession.AuthRole()

	switch msg.Type() {
	case messages.MessageTypeCall:
		return role == "callRole", nil
	case messages.MessageTypeRegister:
		return role == "registerRole", nil
	case messages.MessageTypePublish:
		return role == "publishRole", nil
	case messages.MessageTypeSubscribe:
		return role == "subscribeRole", nil
	default:
		return false, nil
	}
}

func TestCustomAuthorizer(t *testing.T) {
	router := xconn.NewRouter()
	router.AddRealm(realmName)

	err := router.SetRealmAuthorizer(realmName, &testAuthorizer{})
	require.NoError(t, err)

	createSession := func(role string) *xconn.Session {
		authID := fmt.Sprintf("%012x", rand.Uint64())[:12] // #nosec
		baseSession, err := xconn.ConnectInMemoryBase(router, realmName, authID, role, &serializers.JSONSerializer{})
		require.NoError(t, err)
		return xconn.NewSession(baseSession, baseSession.Serializer())
	}

	t.Run("AllowRegister", func(t *testing.T) {
		s := createSession("registerRole")
		resp := s.Register("io.xconn.test", func(ctx context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult()
		}).Do()
		require.NoError(t, resp.Err)
	})

	t.Run("DenyRegister", func(t *testing.T) {
		s := createSession("denied")
		resp := s.Register("io.xconn.test", func(ctx context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult()
		}).Do()
		require.EqualError(t, resp.Err, "wamp.error.authorization_failed")
	})

	t.Run("AllowCall", func(t *testing.T) {
		s := createSession("callRole")
		resp := s.Call("io.xconn.test").Do()
		require.NoError(t, resp.Err)
	})

	t.Run("DenyCall", func(t *testing.T) {
		s := createSession("denied")
		resp := s.Call("io.xconn.test").Do()
		require.EqualError(t, resp.Err, "wamp.error.authorization_failed")
	})

	t.Run("AllowSubscribe", func(t *testing.T) {
		s := createSession("subscribeRole")
		resp := s.Subscribe("io.xconn.test", func(e *xconn.Event) {}).Do()
		require.NoError(t, resp.Err)
	})

	t.Run("DenySubscribe", func(t *testing.T) {
		s := createSession("denied")
		resp := s.Subscribe("io.xconn.test", func(e *xconn.Event) {}).Do()
		require.EqualError(t, resp.Err, "wamp.error.authorization_failed")
	})

	t.Run("AllowPublish", func(t *testing.T) {
		s := createSession("publishRole")
		resp := s.Publish("io.xconn.test").Acknowledge(true).Do()
		require.NoError(t, resp.Err)
	})

	t.Run("DenyPublish", func(t *testing.T) {
		s := createSession("denied")
		resp := s.Publish("io.xconn.test").Acknowledge(true).Do()
		require.EqualError(t, resp.Err, "wamp.error.authorization_failed")
	})
}

func testBlockedClient(
	t *testing.T,
	scheme string,
	startServer func(*xconn.Server, string) (*xconn.Listener, error),
	dial func(context.Context, *netURL.URL) (xconn.Peer, error),
) {
	t.Helper()

	// start server
	router := xconn.NewRouter()
	require.NoError(t, router.AddRealm("realm1"))
	err := router.AddRealmRole("realm1", xconn.RealmRole{
		Name: "anonymous",
		Permissions: []xconn.Permission{
			{
				URI:            "",
				MatchPolicy:    "prefix",
				AllowCall:      true,
				AllowRegister:  true,
				AllowPublish:   true,
				AllowSubscribe: true,
			}},
	})
	require.NoError(t, err)
	server := xconn.NewServer(router, nil, nil)

	listener, err := startServer(server, "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	logrus.SetLevel(logrus.DebugLevel)

	// subscribe to topic 'io.xconn.test'
	parsedURL, err := netURL.Parse(fmt.Sprintf("%s://%s", scheme, listener.Addr()))
	require.NoError(t, err)

	peer, err := dial(context.Background(), parsedURL)
	require.NoError(t, err)

	baseSession, err := xconn.Join(peer, "realm1", &serializers.CBORSerializer{},
		auth.NewAnonymousAuthenticator("", nil))
	require.NoError(t, err)

	require.NoError(t, baseSession.WriteMessage(messages.NewSubscribe(1, nil, "io.xconn.test")))

	msg, err := baseSession.ReadMessage()
	require.NoError(t, err)
	_, ok := msg.(*messages.Subscribed)
	require.True(t, ok)

	// register a procedure
	require.NoError(t, baseSession.WriteMessage(messages.NewRegister(1, nil, "io.xconn.test")))
	msg1, err := baseSession.ReadMessage()
	require.NoError(t, err)
	_, ok1 := msg1.(*messages.Registered)
	require.True(t, ok1)

	// publisher client
	session, err := xconn.ConnectAnonymous(context.Background(),
		fmt.Sprintf("%s://%s", scheme, listener.Addr()), "realm1")
	require.NoError(t, err)

	// publish large data payload 100 times
	data := make([]byte, 1024*1024)
	_, err = crand.Read(data)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		resp := session.Publish("io.xconn.test").Acknowledge(true).Arg(data).Do()
		require.NoError(t, resp.Err)
	}

	// The callee is blocked so the router should respond with an error
	callResp := session.Call("io.xconn.test").Do()
	require.EqualError(t, callResp.Err, "wamp.error.network_failure: callee blocked, cannot call procedure")

	err = session.Leave()
	require.NoError(t, err)
}

func TestBlockedRawSocketClient(t *testing.T) {
	testBlockedClient(t, "rs", func(server *xconn.Server, address string) (*xconn.Listener, error) {
		return server.ListenAndServeRawSocket(xconn.NetworkTCP, address)
	}, func(ctx context.Context, url *netURL.URL) (xconn.Peer, error) {
		return xconn.DialRawSocket(ctx, url, &xconn.RawSocketDialerConfig{
			Serializer: transports.SerializerCbor,
		})
	})
}

func TestBlockedWebSocketClient(t *testing.T) {
	testBlockedClient(t, "ws", func(server *xconn.Server, address string) (*xconn.Listener, error) {
		return server.ListenAndServeWebSocket(xconn.NetworkTCP, address)
	}, func(ctx context.Context, url *netURL.URL) (xconn.Peer, error) {
		peer, _, err := xconn.DialWebSocket(ctx, url, &xconn.WSDialerConfig{
			SubProtocols: []string{xconn.CBORSerializerSpec.SubProtocol()},
		})
		return peer, err
	})
}

func blockedCaller(t *testing.T, publishCount int) xconn.BaseSession {
	t.Helper()

	router := xconn.NewRouter()
	require.NoError(t, router.AddRealm("realm1"))
	err := router.AddRealmRole("realm1", xconn.RealmRole{
		Name: "anonymous",
		Permissions: []xconn.Permission{
			{
				URI:            "",
				MatchPolicy:    "prefix",
				AllowCall:      true,
				AllowRegister:  true,
				AllowPublish:   true,
				AllowSubscribe: true,
			}},
	})
	require.NoError(t, err)
	server := xconn.NewServer(router, nil, nil)

	listener, err := server.ListenAndServeWebSocket(xconn.NetworkTCP, "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	logrus.SetLevel(logrus.DebugLevel)

	parsedURL, err := netURL.Parse(fmt.Sprintf("ws://%s", listener.Addr()))
	require.NoError(t, err)

	// --- CLIENT 1 SETUP (Publisher + Callee) ---
	client1, err := xconn.ConnectAnonymous(context.Background(), parsedURL.String(), "realm1")
	require.NoError(t, err)
	require.NotNil(t, client1)

	callReceived := make(chan struct{})
	response := client1.Register(
		"io.xconn.get", func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			defer func() {
				callReceived <- struct{}{}
			}()
			return xconn.NewInvocationResult()
		}).Do()
	require.NoError(t, response.Err)

	// --- CLIENT 2 SETUP (Subscriber + Caller) ---

	// Establish a low-level WebSocket connection for Client 2.
	// We use a raw baseSession here to simulate a "dumb" client
	// with no backpressure handling (it won't read messages fast enough).

	peer, _, err := xconn.DialWebSocket(context.Background(), parsedURL, &xconn.WSDialerConfig{
		SubProtocols: []string{xconn.CBORSerializerSpec.SubProtocol()},
	})
	require.NoError(t, err)

	baseSession, err := xconn.Join(peer, "realm1", &serializers.CBORSerializer{},
		auth.NewAnonymousAuthenticator("", nil))
	require.NoError(t, err)

	require.NoError(t, baseSession.WriteMessage(messages.NewSubscribe(1, nil, "io.xconn.test")))

	msg, err := baseSession.ReadMessage()
	require.NoError(t, err)
	_, ok := msg.(*messages.Subscribed)
	require.True(t, ok)

	data := make([]byte, 1024)
	_, err = crand.Read(data)
	require.NoError(t, err)

	for i := 0; i < publishCount; i++ {
		resp := client1.Publish("io.xconn.test").Acknowledge(true).Arg(data).Do()
		require.NoError(t, resp.Err)
	}

	require.NoError(t, baseSession.WriteMessage(messages.NewCall(2, nil, "io.xconn.get", nil, nil)))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	select {
	case <-callReceived:
	case <-ctx.Done():
		t.Fatal("timeout waiting for call")
	}

	return baseSession
}

func TestBlockedCaller(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		baseSessionClient2 := blockedCaller(t, 64)

		// now read all pending messages from the router, we know "RESULT" won't be there as it got dropped
		for i := 0; i < 64; i++ {
			msg, err := baseSessionClient2.ReadMessage()
			require.NoError(t, err)

			_, ok := msg.(*messages.Event)
			require.True(t, ok)
		}
	})

	t.Run("notblocked", func(t *testing.T) {
		baseSessionClient2 := blockedCaller(t, 63)

		// now read all pending messages from the router, we know "RESULT" won't be there as it got dropped
		for i := 0; i < 64; i++ {
			msg, err := baseSessionClient2.ReadMessage()
			require.NoError(t, err)
			if i == 63 {
				_, ok := msg.(*messages.Result)
				require.True(t, ok)
			} else {
				_, ok := msg.(*messages.Event)
				require.True(t, ok)
			}
		}
	})
}
