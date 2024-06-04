package xconn_test

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/gammazero/nexus/v3/client"
	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go/serializers"
	wampprotobuf "github.com/xconnio/wampproto-protobuf/go"
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

		acceptor := xconn.WebSocketAcceptor{}
		session, err := acceptor.Accept(conn)
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

func TestProtobuf(t *testing.T) {
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	require.NotNil(t, listener)

	connections := 50000

	msgpackSerializer := &serializers.MsgPackSerializer{}
	msgpackSpec := xconn.NewWSSerializerSpec("wamp.2.msgpack", msgpackSerializer)
	fmt.Println(msgpackSpec)

	protobufSerializer := &wampprotobuf.ProtobufSerializer{}
	protobufSpec := xconn.NewWSSerializerSpec("wamp.2.protobuf", protobufSerializer)

	acceptor := &xconn.WebSocketAcceptor{}
	err = acceptor.RegisterSpec(protobufSpec.SubProtocol(), protobufSpec.Serializer())
	require.NoError(t, err)

	go func() {
		for i := 0; i < connections; i++ {
			conn, err := listener.Accept()
			require.NoError(t, err)
			require.NotNil(t, conn)

			go func() {
				session, err := acceptor.Accept(conn)
				require.NoError(t, err)
				require.NotNil(t, session)
			}()
		}
	}()

	wsURL := fmt.Sprintf("ws://%s/ws", listener.Addr().String())

	for i := 0; i < connections; i++ {
		joiner := &xconn.WebSocketJoiner{
			SerializerSpec: protobufSpec,
		}

		base, err := joiner.Join(context.Background(), wsURL, "realm1")
		require.NoError(t, err)
		require.NotNil(t, base)
	}
}
