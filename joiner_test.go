package xconn_test

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/gammazero/nexus/v3/router"
	"github.com/gammazero/nexus/v3/wamp"
	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func startRouter(t *testing.T, realm string) net.Listener {
	r, err := router.NewRouter(&router.Config{RealmConfigs: []*router.RealmConfig{
		{URI: wamp.URI(realm), AnonymousAuth: true},
	}}, nil)

	require.NoError(t, err)
	require.NotNil(t, r)

	server := router.NewWebsocketServer(r)
	closer, err := server.ListenAndServe("localhost:0")
	require.NoError(t, err)
	require.NotNil(t, closer)

	listener := closer.(net.Listener)
	require.NotNil(t, listener)
	return listener
}

func TestJoin(t *testing.T) {
	listener := startRouter(t, "realm1")
	defer func() { _ = listener.Close() }()
	address := fmt.Sprintf("ws://%s/ws", listener.Addr().String())

	var joiner xconn.WebSocketJoiner
	base, err := joiner.Join(context.Background(), address, "realm1")
	require.NoError(t, err)
	require.NotNil(t, base)

	require.Equal(t, "realm1", base.Realm())
	require.Equal(t, "anonymous", base.AuthRole())
}
