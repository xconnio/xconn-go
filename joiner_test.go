package xconn_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func TestJoin(t *testing.T) {
	router := initRouterWithRealm1(t)
	server := xconn.NewServer(router, nil, nil)

	listener, err := server.ListenAndServeWebSocket(xconn.NetworkTCP, "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	address := fmt.Sprintf("ws://%s/ws", listener.Addr().String())

	var joiner xconn.WebSocketJoiner
	base, err := joiner.Join(context.Background(), address, "realm1", &xconn.WSDialerConfig{
		SubProtocols: []string{xconn.JsonWebsocketProtocol},
	})
	require.NoError(t, err)
	require.NotNil(t, base)

	require.Equal(t, "realm1", base.Realm())
	require.Equal(t, "anonymous", base.AuthRole())
}
