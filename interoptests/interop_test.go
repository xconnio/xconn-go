package interoptests_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/util"
	"github.com/xconnio/xconn-go"
)

const (
	xconnURL          = "ws://localhost:8080/ws"
	xconnRawSockerURI = "unix:///tmp/nxt.sock"
	crossbarURL       = "ws://localhost:8081/ws"
	realm             = "realm1"
	procedureAdd      = "io.xconn.backend.add2"

	ticketUserAuthID = "ticket-user"
	ticket           = "ticket-pass"

	craUserAuthID = "wamp-cra-user"
	secret        = "cra-secret"

	cryptosignUserAuthID = "cryptosign-user"
	privateKey           = "150085398329d255ad69e82bf47ced397bcec5b8fbeecd28a80edbbd85b49081"
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

	callResponse := session.Call(procedureAdd).Args(2, 2).Do()
	require.NoError(t, callResponse.Err)

	sumResult, ok := util.AsUInt64(callResponse.Args.Raw()[0])
	require.True(t, ok)
	require.Equal(t, 4, int(sumResult))
}

func testRPC(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.SerializerSpec, url string) {
	session := connectSession(t, authenticator, serializer, url)

	registerResponse := session.Register("io.xconn.test",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			return &xconn.InvocationResult{Args: invocation.Args(), Kwargs: invocation.Kwargs()}
		}).Do()
	require.NoError(t, registerResponse.Err)

	args := []any{"Hello", "wamp"}
	callResponse := session.Call("io.xconn.test").Args(args...).Do()
	require.NoError(t, callResponse.Err)
	require.Equal(t, args, callResponse.Args.Raw())

	err := registerResponse.Unregister()
	require.NoError(t, err)
}

func testPubSub(t *testing.T, authenticator auth.ClientAuthenticator, serializer xconn.SerializerSpec, url string) {
	session := connectSession(t, authenticator, serializer, url)

	args := []any{"Hello", "wamp"}
	subscribeResponse := session.Subscribe("io.xconn.test", func(event *xconn.Event) {
		require.Equal(t, args, event.Args())
	}).Do()
	require.NoError(t, subscribeResponse.Err)

	publishResponse := session.Publish("io.xconn.test").Args(args...).Option("acknowledge", true).Do()
	require.NoError(t, publishResponse.Err)

	err := subscribeResponse.Unsubscribe()
	require.NoError(t, err)
}

func TestInteroperability(t *testing.T) {
	serverURLs := map[string]string{
		"XConn":          xconnURL,
		"XconnRawSocket": xconnRawSockerURI,
		"Crossbar":       crossbarURL,
	}

	cryptosignAuthenticator, err := auth.NewCryptoSignAuthenticator(cryptosignUserAuthID, privateKey, map[string]any{})
	require.NoError(t, err)

	authenticators := map[string]auth.ClientAuthenticator{
		"AnonymousAuth":     auth.NewAnonymousAuthenticator("", map[string]any{}),
		"TicketAuth":        auth.NewTicketAuthenticator(ticketUserAuthID, ticket, map[string]any{}),
		"WAMPCRAAuth":       auth.NewWAMPCRAAuthenticator(craUserAuthID, secret, map[string]any{}),
		"WAMPCRAAuthSalted": auth.NewWAMPCRAAuthenticator("wamp-cra-salt-user", "cra-salt-secret", map[string]any{}),
		"CryptosignAuth":    cryptosignAuthenticator,
	}

	for serverName, url := range serverURLs {
		serializers := map[string]xconn.SerializerSpec{
			"JSON":    xconn.JSONSerializerSpec,
			"CBOR":    xconn.CBORSerializerSpec,
			"MsgPack": xconn.MsgPackSerializerSpec,
		}

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

	for serverName, uri := range serverURLs {
		t.Run("AnonymousAuthWith"+serverName, func(t *testing.T) {
			session, err := xconn.ConnectAnonymous(context.Background(), uri, realm)
			require.NoError(t, err)
			require.NotNil(t, session)

			require.NoError(t, session.Leave())
		})

		t.Run("TicketAuthWith"+serverName, func(t *testing.T) {
			session, err := xconn.ConnectTicket(context.Background(), uri, realm, ticketUserAuthID, ticket)
			require.NoError(t, err)
			require.NotNil(t, session)

			require.NoError(t, session.Leave())
		})

		t.Run("CRAAuthWith"+serverName, func(t *testing.T) {
			session, err := xconn.ConnectCRA(context.Background(), uri, realm, craUserAuthID, secret)
			require.NoError(t, err)
			require.NotNil(t, session)

			require.NoError(t, session.Leave())
		})

		t.Run("CryptosignAuthWith"+serverName, func(t *testing.T) {
			session, err := xconn.ConnectCryptosign(context.Background(), uri, realm, cryptosignUserAuthID, privateKey)
			require.NoError(t, err)
			require.NotNil(t, session)

			require.NoError(t, session.Leave())
		})
	}
}
