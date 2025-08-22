package xconn_test

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/gammazero/nexus/v3/client"
	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func TestAccept(t *testing.T) {
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	require.NotNil(t, listener)

	accepted := make(chan xconn.BaseSession, 1)

	go func() {
		conn, err := listener.Accept()
		require.NoError(t, err)
		require.NotNil(t, conn)

		rout := xconn.NewRouter()
		err = rout.AddRealm("realm1")
		require.NoError(t, err)

		acceptor := xconn.WebSocketAcceptor{}
		session, err := acceptor.Accept(conn, rout, nil)
		require.NoError(t, err)
		require.NotNil(t, session)

		accepted <- session
	}()

	wsURL := fmt.Sprintf("ws://%s/ws", listener.Addr().String())
	config := client.Config{Realm: "realm1"}
	cl, err := client.ConnectNet(context.Background(), wsURL, config)
	require.NoError(t, err)
	require.NotNil(t, cl)
}
