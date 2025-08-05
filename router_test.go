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

func TestRouterMetaKill(t *testing.T) {
	realmName := "test"
	router := xconn.NewRouter()
	router.AddRealm(realmName)
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

func TestAuthorization(t *testing.T) {
	realmName := "test"
	router := xconn.NewRouter()
	router.AddRealm(realmName)

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
	realmName := "authRealm"
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
