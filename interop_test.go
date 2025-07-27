package xconn_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/util"
	"github.com/xconnio/xconn-go"
)

const (
	xconnURL     = "ws://localhost:8080/ws"
	crossbarURL  = "ws://localhost:8081/ws"
	realm        = "realm1"
	procedureAdd = "io.xconn.backend.add2"
)

func connectSession(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.SerializerSpec,
	url string) *xconn.Session {
	client := xconn.Client{
		Authenticator:  authenticator,
		SerializerSpec: serializer,
	}

	session, err := client.Connect(context.Background(), url, realm)
	require.NoError(t, err)

	return session
}

func testCall(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.SerializerSpec, url string) {
	session := connectSession(t, authenticator, serializer, url)

	callRequest := xconn.NewCallRequest(procedureAdd).Args(2, 2)
	result, err := session.CallWithRequest(context.Background(), callRequest)
	require.NoError(t, err)

	sumResult, ok := util.AsUInt64(result.Arguments[0])
	require.True(t, ok)
	require.Equal(t, 4, int(sumResult))
}

func testRPC(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.SerializerSpec, url string) {
	session := connectSession(t, authenticator, serializer, url)

	registerRequest := xconn.NewRegisterRequest("io.xconn.test",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.Result {
			return &xconn.Result{Arguments: invocation.Arguments, KwArguments: invocation.KwArguments}
		})
	reg, err := session.RegisterWithRequest(registerRequest)
	require.NoError(t, err)

	args := []any{"Hello", "wamp"}
	callRequest := xconn.NewCallRequest("io.xconn.test").Args(args...)
	result, err := session.CallWithRequest(context.Background(), callRequest)
	require.NoError(t, err)
	require.Equal(t, args, result.Arguments)

	err = reg.Unregister()
	require.NoError(t, err)
}

func testPubSub(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.SerializerSpec, url string) {
	session := connectSession(t, authenticator, serializer, url)

	args := []any{"Hello", "wamp"}
	subscribeRequest := xconn.NewSubscribeRequest("io.xconn.test", func(event *xconn.Event) {
		require.Equal(t, args, event.Arguments)
	})
	sub, err := session.SubscribeWithRequest(subscribeRequest)
	require.NoError(t, err)

	publishRequest := xconn.NewPublishRequest("io.xconn.test").Args(args...).Option("acknowledge", true)
	err = session.PublishWithRequest(publishRequest)
	require.NoError(t, err)

	err = sub.Unsubscribe()
	require.NoError(t, err)
}

func TestInteroperability(t *testing.T) {
	serverURLs := map[string]string{
		"XConn":    xconnURL,
		"Crossbar": crossbarURL,
	}

	cryptosignAuthenticator, err := auth.NewCryptoSignAuthenticator(
		"cryptosign-user",
		"150085398329d255ad69e82bf47ced397bcec5b8fbeecd28a80edbbd85b49081",
		map[string]any{},
	)
	require.NoError(t, err)

	authenticators := map[string]auth.ClientAuthenticator{
		"AnonymousAuth":     auth.NewAnonymousAuthenticator("", map[string]any{}),
		"TicketAuth":        auth.NewTicketAuthenticator("ticket-user", "ticket-pass", map[string]any{}),
		"WAMPCRAAuth":       auth.NewCRAAuthenticator("wamp-cra-user", "cra-secret", map[string]any{}),
		"WAMPCRAAuthSalted": auth.NewCRAAuthenticator("wamp-cra-salt-user", "cra-salt-secret", map[string]any{}),
		"CryptosignAuth":    cryptosignAuthenticator,
	}

	serializers := map[string]xconn.SerializerSpec{
		"JSON":    xconn.JSONSerializerSpec,
		"CBOR":    xconn.CBORSerializerSpec,
		"MsgPack": xconn.MsgPackSerializerSpec,
	}

	for serverName, url := range serverURLs {
		for authName, authenticator := range authenticators {
			for serializerName, serializer := range serializers {
				t.Run(serverName+"With"+authName+"And"+serializerName, func(t *testing.T) {
					testCall(t, authenticator, serializer, url)
					testRPC(t, authenticator, serializer, url)
					testPubSub(t, authenticator, serializer, url)
				})
			}
		}
	}
}
