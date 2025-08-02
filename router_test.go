package xconn_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go"
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
