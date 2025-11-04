package xconn_test

import (
	"context"
	"fmt"
	"net"
	"testing"

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

		rout, err := xconn.NewRouter(nil)
		require.NoError(t, err)
		err = rout.AddRealm("realm1", &xconn.RealmConfig{})
		require.NoError(t, err)

		acceptor := xconn.WebSocketAcceptor{}
		session, err := acceptor.Accept(conn, rout, nil)
		require.NoError(t, err)
		require.NotNil(t, session)

		accepted <- session
	}()

	wsURL := fmt.Sprintf("ws://%s/ws", listener.Addr().String())

	session, err := xconn.ConnectAnonymous(context.Background(), wsURL, "realm1")
	require.NoError(t, err)
	require.NotNil(t, session)
}
