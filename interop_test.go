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

func connectSession(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.WSSerializerSpec,
	url string) *xconn.Session {
	client := xconn.Client{
		Authenticator:  authenticator,
		SerializerSpec: serializer,
	}

	session, err := client.Connect(context.Background(), url, realm)
	require.NoError(t, err)

	return session
}

func testCall(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.WSSerializerSpec, url string) {
	session := connectSession(t, authenticator, serializer, url)

	result, err := session.Call(context.Background(), procedureAdd, []any{2, 2}, nil, nil)
	require.NoError(t, err)

	sumResult, ok := util.AsInt64(result.Arguments[0])
	require.True(t, ok)
	require.Equal(t, 4, int(sumResult))
}

func testRPC(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.WSSerializerSpec, url string) {
	session := connectSession(t, authenticator, serializer, url)

	reg, err := session.Register("io.xconn.test", func(ctx context.Context, invocation *xconn.Invocation) *xconn.Result {
		return &xconn.Result{Arguments: invocation.Arguments, KwArguments: invocation.KwArguments}
	}, nil)
	require.NoError(t, err)

	args := []any{"Hello", "wamp"}
	result, err := session.Call(context.Background(), "io.xconn.test", args, nil, nil)
	require.NoError(t, err)
	require.Equal(t, args, result.Arguments)

	err = session.Unregister(reg.ID)
	require.NoError(t, err)
}

func testPubSub(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.WSSerializerSpec, url string) {
	session := connectSession(t, authenticator, serializer, url)

	args := []any{"Hello", "wamp"}
	sub, err := session.Subscribe("io.xconn.test", func(event *xconn.Event) {
		require.Equal(t, args, event.Arguments)
	}, nil)
	require.NoError(t, err)

	err = session.Publish("io.xconn.test", args, nil, map[string]any{"acknowledge": true})
	require.NoError(t, err)

	err = session.Unsubscribe(sub)
	require.NoError(t, err)
}

func TestInteroperability(t *testing.T) {
	serverURLs := map[string]string{
		"XConn":    xconnURL,
		"Crossbar": crossbarURL,
	}

	cryptosignAuthenticator, err := auth.NewCryptoSignAuthenticator(
		"cryptosign-user",
		map[string]any{},
		"150085398329d255ad69e82bf47ced397bcec5b8fbeecd28a80edbbd85b49081",
	)
	require.NoError(t, err)

	authenticators := map[string]auth.ClientAuthenticator{
		"AnonymousAuth": auth.NewAnonymousAuthenticator("", map[string]any{}),
		"TicketAuth":    auth.NewTicketAuthenticator("ticket-user", map[string]any{}, "ticket-pass"),
		"WAMPCRAAuth":   auth.NewCRAAuthenticator("wamp-cra-user", map[string]any{}, "cra-secret"),
		// FIXME: WAMPCRA with salt is broken in crossbar
		//"WAMPCRAAuthSalted": auth.NewCRAAuthenticator("wamp-cra-salt-user", map[string]any{}, "cra-salt-secret"),
		"CryptosignAuth": cryptosignAuthenticator,
	}

	serializers := map[string]xconn.WSSerializerSpec{
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
