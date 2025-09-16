package xconn_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
	"github.com/xconnio/xconn-go"
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
	require.False(t, subResponse.IsError())

	leaveChan := make(chan *xconn.Event)
	subResponse = session1.Subscribe(xconn.MetaTopicSessionLeave, func(event *xconn.Event) {
		leaveChan <- event
	}).Do()
	require.False(t, subResponse.IsError())

	session2, err := xconn.ConnectInMemory(router, realmName)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		<-joinChan
		return true
	}, 1*time.Second, 50*time.Millisecond)

	response := session1.Call(xconn.MetaProcedureSessionKill).Args(session2.ID()).Do()
	require.False(t, response.IsError())

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
	require.False(t, resp.IsError())
	sessionList, err := resp.Args.List(0)
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
	require.EqualError(t, resp.Error(), "wamp.error.invalid_argument: index 0 out of range [0, 0]")
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
	require.False(t, resp.IsError())
	sessionList, err := resp.Args.List(0)
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
	require.EqualError(t, resp.Error(), "wamp.error.invalid_argument: index 0 out of range [0, 0]")
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
	require.False(t, resp.IsError())
	sessionList, err := resp.Args.List(0)
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
		require.False(t, resp.IsError())
		require.Equal(t, uint64(2), resp.Args.UInt64Or(0, 0))
	})

	// Connect second session with role=admin
	baseSession, err := xconn.ConnectInMemoryBase(router, realmName, "test", "admin", &serializers.JSONSerializer{})
	require.NoError(t, err)
	session2 := xconn.NewSession(baseSession, baseSession.Serializer())

	t.Run("CountAllSessions", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionCount).Do()
		require.False(t, resp.IsError())
		require.Equal(t, uint64(3), resp.Args.UInt64Or(0, 0))
	})

	t.Run("CountOnlyAdminSessions", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionCount).Arg([]any{"admin"}).Do()
		require.False(t, resp.IsError())
		require.Equal(t, uint64(1), resp.Args.UInt64Or(0, 0))
	})

	// Disconnect admin session
	require.NoError(t, session2.Leave())

	t.Run("CountAfterAdminSessionLeaves", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionCount).Do()
		require.False(t, resp.IsError())
		require.Equal(t, uint64(2), resp.Args.UInt64Or(0, 0))
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
		require.False(t, resp.IsError())
		ids := resp.Args.ListOr(0, nil)
		require.Len(t, ids, 2)
	})

	// Connect second session with role=admin
	baseSession, err := xconn.ConnectInMemoryBase(router, realmName, "test", "admin", &serializers.JSONSerializer{})
	require.NoError(t, err)
	session2 := xconn.NewSession(baseSession, baseSession.Serializer())

	t.Run("ListAllSessions", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionList).Do()
		require.False(t, resp.IsError())
		ids := resp.Args.ListOr(0, nil)
		require.Len(t, ids, 3)
	})

	t.Run("ListOnlyAdminSessions", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionList).Arg([]any{"admin"}).Do()
		require.False(t, resp.IsError())
		ids := resp.Args.ListOr(0, nil)
		require.Len(t, ids, 1)
		require.Equal(t, session2.ID(), ids[0])
	})

	// Disconnect admin session
	require.NoError(t, session2.Leave())

	t.Run("ListAfterAdminSessionLeaves", func(t *testing.T) {
		resp := session.Call(xconn.MetaProcedureSessionList).Do()
		require.False(t, resp.IsError())
		ids := resp.Args.ListOr(0, nil)
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
	require.False(t, resp.IsError())
	require.Equal(t, expectedDetails, resp.Args.Raw()[0])

	// test err
	respErr := session.Call(xconn.MetaProcedureSessionGet).Arg(uint64(2152454520)).Do()
	require.Equal(t, "wamp.error.no_such_session: invalid session id", respErr.Error().Error())
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
		require.False(t, registerResp.IsError())

		callResp := session.Call("io.xconn.test").Do()
		require.False(t, callResp.IsError())

		publishResp := session.Publish("io.xconn.test").Acknowledge(true).Do()
		require.Equal(t, publishResp.Error().Error(), "wamp.error.authorization_failed")

		subscribeResp := session.Subscribe("io.xconn.test", func(event *xconn.Event) {}).Do()
		require.Equal(t, subscribeResp.Error().Error(), "wamp.error.authorization_failed")
	})

	t.Run("AllowPublish", func(t *testing.T) {
		addRole("publishOnly", []xconn.Permission{{
			URI:          "io.xconn.",
			MatchPolicy:  wampproto.MatchPrefix,
			AllowPublish: true,
		}})
		session := createSession("publishOnly")

		callResp := session.Call("io.xconn.test").Do()
		require.Equal(t, callResp.Error().URI, "wamp.error.authorization_failed")

		publishResp := session.Publish("io.xconn.test").Do()
		require.False(t, publishResp.IsError())

		subscribeResp := session.Subscribe("io.xconn.test", func(event *xconn.Event) {}).Do()
		require.Equal(t, subscribeResp.Error().Error(), "wamp.error.authorization_failed")

		registerResp := session.Register("io.xconn.test",
			func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
				return &xconn.InvocationResult{}
			}).Do()
		require.Equal(t, registerResp.Error().Error(), "wamp.error.authorization_failed")
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
		require.Equal(t, registerResp.Error().Error(), "wamp.error.authorization_failed")

		callResp := session.Call("io.xconn.test").Do()
		require.Equal(t, callResp.Error().URI, "wamp.error.authorization_failed")

		subscribeResp := session.Subscribe("io.xconn.test", func(event *xconn.Event) {}).Do()
		require.False(t, subscribeResp.IsError())

		publishResp := session.Publish("io.xconn.test").Acknowledge(true).Do()
		require.False(t, publishResp.IsError())
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
		require.False(t, resp.IsError())
	})

	t.Run("DenyRegister", func(t *testing.T) {
		s := createSession("denied")
		resp := s.Register("io.xconn.test", func(ctx context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult()
		}).Do()
		require.EqualError(t, resp.Error(), "wamp.error.authorization_failed")
	})

	t.Run("AllowCall", func(t *testing.T) {
		s := createSession("callRole")
		resp := s.Call("io.xconn.test").Do()
		require.False(t, resp.IsError())
	})

	t.Run("DenyCall", func(t *testing.T) {
		s := createSession("denied")
		resp := s.Call("io.xconn.test").Do()
		require.EqualError(t, resp.Error(), "wamp.error.authorization_failed")
	})

	t.Run("AllowSubscribe", func(t *testing.T) {
		s := createSession("subscribeRole")
		resp := s.Subscribe("io.xconn.test", func(e *xconn.Event) {}).Do()
		require.False(t, resp.IsError())
	})

	t.Run("DenySubscribe", func(t *testing.T) {
		s := createSession("denied")
		resp := s.Subscribe("io.xconn.test", func(e *xconn.Event) {}).Do()
		require.EqualError(t, resp.Error(), "wamp.error.authorization_failed")
	})

	t.Run("AllowPublish", func(t *testing.T) {
		s := createSession("publishRole")
		resp := s.Publish("io.xconn.test").Acknowledge(true).Do()
		require.False(t, resp.IsError())
	})

	t.Run("DenyPublish", func(t *testing.T) {
		s := createSession("denied")
		resp := s.Publish("io.xconn.test").Acknowledge(true).Do()
		require.EqualError(t, resp.Error(), "wamp.error.authorization_failed")
	})
}
