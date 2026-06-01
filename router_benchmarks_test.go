package xconn_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func startWebsocketSever(b *testing.B) string {
	router := initRouterWithRealm1(b)

	server := xconn.NewServer(router, nil, nil)
	listener, err := server.ListenAndServeWebSocket(xconn.NetworkTCP, "localhost:0")
	require.NoError(b, err)
	b.Cleanup(func() { _ = listener.Close() })

	return fmt.Sprintf("ws://%s", listener.Addr())
}

func startRawSocketSever(b *testing.B) string {
	router := initRouterWithRealm1(b)

	server := xconn.NewServer(router, nil, nil)
	listener, err := server.ListenAndServeRawSocket(xconn.NetworkTCP, "localhost:0")
	require.NoError(b, err)
	b.Cleanup(func() { _ = listener.Close() })

	return fmt.Sprintf("rs://%s", listener.Addr())
}

func startRawSocketSeverOverUnix(b *testing.B) string {
	router := initRouterWithRealm1(b)

	server := xconn.NewServer(router, nil, nil)
	listener, err := server.ListenAndServeRawSocket(xconn.NetworkUnix, "/home/muzzammil/test.sock")
	require.NoError(b, err)
	b.Cleanup(func() { _ = listener.Close() })

	return fmt.Sprintf("unix://%s", listener.Addr())
}

func BenchmarkRouterCall(b *testing.B) {
	transports := map[string]func(b *testing.B) string{
		"WebSocket":     startWebsocketSever,
		"RawSocket":     startRawSocketSever,
		"RawSocketUnix": startRawSocketSeverOverUnix,
	}

	// 1KB, 10KB, 100KB, 500KB, 1MB, 2MB, 5MB, 10MB
	payloadSizes := []int{1 * 1024, 10 * 1024, 100 * 1024, 500 * 1024, 1 * 1024 * 1024, 2 * 1024 * 1024, 5 * 1024 * 1024,
		10 * 1024 * 1024}

	for transportName, transport := range transports {
		b.Run(transportName, func(b *testing.B) {
			url := transport(b)

			// Setup callee
			callee, err := xconn.ConnectAnonymous(context.Background(), url, "realm1")
			require.NoError(b, err)
			regResp := callee.Register("io.xconn.test",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					return xconn.NewInvocationResult()
				}).Do()
			require.NoError(b, regResp.Err)

			// Setup caller
			caller, err := xconn.ConnectAnonymous(context.Background(), url, "realm1")
			require.NoError(b, err)

			for _, size := range payloadSizes {
				var label string
				if size >= 1024*1024 {
					label = fmt.Sprintf("%s%dMB", transportName, size/(1024*1024))
				} else {
					label = fmt.Sprintf("%s%dKB", transportName, size/1024)
				}

				b.Run(label, func(b *testing.B) {
					data := make([]byte, size)
					for i := range data {
						data[i] = byte(i % 256)
					}

					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						callResp := caller.Call("io.xconn.test").Arg(data).Do()
						require.NoError(b, callResp.Err)
					}
				})
			}
		})
	}
}
