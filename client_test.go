package xconn_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go/serializers"
	"github.com/xconnio/wampproto-go/util"
	"github.com/xconnio/xconn-go"
)

const msgPackSerializer = "msgpack"

func forEachSerializer(fn func(name string, spec xconn.SerializerSpec)) {
	serializersToTest := []struct {
		name string
		spec xconn.SerializerSpec
	}{
		{"json", xconn.JSONSerializerSpec},
		{"cbor", xconn.CBORSerializerSpec},
		{"msgpack", xconn.MsgPackSerializerSpec},
	}

	for _, s := range serializersToTest {
		fn(s.name, s.spec)
	}
}

func connect(t *testing.T, serializerSpec xconn.SerializerSpec) *xconn.Session {
	listener := startRouter(t, "realm1")
	defer func() { _ = listener.Close() }()
	address := fmt.Sprintf("ws://%s/ws", listener.Addr().String())

	client := &xconn.Client{
		SerializerSpec: serializerSpec,
	}

	session, err := client.Connect(context.Background(), address, "realm1")
	require.NoError(t, err)
	require.NotNil(t, session)

	return session
}

func connectInMemory(t *testing.T, router *xconn.Router, realm string,
	serializer serializers.Serializer) *xconn.Session {
	authID := fmt.Sprintf("%012x", rand.Uint64())[:12]
	authRole := "trusted"

	base, err := xconn.ConnectInMemoryBase(router, realm, authID, authRole, serializer)
	require.NoError(t, err)

	return xconn.NewSession(base, base.Serializer())
}

func connectedLocalSessions(t *testing.T, serializer serializers.Serializer) (*xconn.Session, *xconn.Session) {
	router := xconn.NewRouter()
	err := router.AddRealm(realmName)
	require.NoError(t, err)

	callee := connectInMemory(t, router, realmName, serializer)

	caller := connectInMemory(t, router, realmName, serializer)
	return callee, caller
}

func TestCall(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		// msgpack is broken in nexus
		if name == msgPackSerializer {
			return
		}
		t.Run("NexusRouterWith"+name, func(t *testing.T) {
			session := connect(t, spec)
			t.Run("CallNoProc", func(t *testing.T) {
				callResponse := session.Call("foo.bar").Do()
				require.Error(t, callResponse.Err)

				var er *xconn.Error
				errors.As(callResponse.Err, &er)
				require.Equal(t, "wamp.error.no_such_procedure", er.URI)
			})
		})
	})
}

func testRegisterCall(t *testing.T, callee, caller *xconn.Session) {
	regResp := callee.Register("io.xconn.test",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult("hello")
		}).Do()
	require.NoError(t, regResp.Err)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			callResponse := caller.Call("io.xconn.test").Do()
			require.NoError(t, callResponse.Err)
			require.NotNil(t, callResponse)
			require.Equal(t, "hello", callResponse.Args.StringOr(0, ""))
		}()
	}
	wg.Wait()

	require.NoError(t, regResp.Unregister())

	callResp := caller.Call("io.xconn.test").Do()
	require.EqualError(t, callResp.Err, "wamp.error.no_such_procedure")
}

func TestRegisterCall(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("NXTRouterWith"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, spec.Serializer())
			testRegisterCall(t, callee, caller)
		})

		// msgpack is broken in nexus
		if name == msgPackSerializer {
			return
		}
		t.Run("NexusRouterWith"+name, func(t *testing.T) {
			session := connect(t, spec)
			testRegisterCall(t, session, session)
		})
	})
}

func testPublishSubscribe(t *testing.T, subscriber, publisher *xconn.Session) {
	eventCh := make(chan *xconn.Event, 100)

	subResp := subscriber.Subscribe("foo.bar",
		func(event *xconn.Event) {
			eventCh <- event
		}).Do()
	require.NoError(t, subResp.Err)

	for i := 0; i < 100; i++ {
		go func() {
			pubResp := publisher.Publish("foo.bar").ExcludeMe(false).Do()
			require.NoError(t, pubResp.Err)
		}()
	}

	for i := 0; i < 100; i++ {
		ev := <-eventCh
		require.NotNil(t, ev)
	}

	require.NoError(t, subResp.Unsubscribe())

	pubResp := publisher.Publish("foo.bar").Do()
	require.NoError(t, pubResp.Err)

	select {
	case <-eventCh:
		t.Fatal("received event after unsubscribe")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPublishSubscribe(t *testing.T) {
	forEachSerializer(func(name string, serializerSpec xconn.SerializerSpec) {
		t.Run("NXTRouterWith"+name, func(t *testing.T) {
			subscriber, publisher := connectedLocalSessions(t, serializerSpec.Serializer())
			testPublishSubscribe(t, subscriber, publisher)
		})

		// msgpack is broken in nexus
		if name == msgPackSerializer {
			return
		}

		t.Run("NexusRouterWith"+name, func(t *testing.T) {
			session := connect(t, serializerSpec)
			testPublishSubscribe(t, session, session)
		})
	})
}

func testProgressiveCallResults(t *testing.T, callee, caller *xconn.Session) {
	registerResponse := callee.Register("foo.bar.progress",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			// Send progress
			for i := 1; i <= 3; i++ {
				err := invocation.SendProgress([]any{i}, nil)
				require.NoError(t, err)
			}

			// Return final result
			return xconn.NewInvocationResult("done")
		}).Do()
	require.NoError(t, registerResponse.Err)

	t.Run("ProgressiveCall", func(t *testing.T) {
		// Store received progress updates
		progressUpdates := make([]int, 0)

		callResponse := caller.Call("foo.bar.progress").ProgressReceiver(func(progressiveResult *xconn.InvocationResult) {
			progress, _ := util.AsInt(progressiveResult.Args[0])
			// Collect received progress
			progressUpdates = append(progressUpdates, progress)
		}).Do()
		require.NoError(t, callResponse.Err)

		// Verify progressive updates received correctly
		require.Equal(t, []int{1, 2, 3}, progressUpdates)

		// Verify the final result
		require.Equal(t, "done", callResponse.Args.StringOr(0, ""))
	})
}

func TestProgressiveCallResults(t *testing.T) {
	forEachSerializer(func(name string, serializerSpec xconn.SerializerSpec) {
		t.Run("NXTRouterWith"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, serializerSpec.Serializer())
			testProgressiveCallResults(t, callee, caller)
		})

		// msgpack is broken in nexus
		if name == msgPackSerializer {
			return
		}

		t.Run("NexusRouterWith"+name, func(t *testing.T) {
			session := connect(t, serializerSpec)
			testProgressiveCallResults(t, session, session)
		})
	})
}

func testProgressiveCallInvocation(t *testing.T, callee, caller *xconn.Session) {
	// Store progress updates
	progressUpdates := make([]int, 0)
	registerResponse := callee.Register("foo.bar.progress",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			progress, _ := util.AsInt(invocation.Args()[0])
			progressUpdates = append(progressUpdates, progress)
			if invocation.Progress() {
				return xconn.NewInvocationError(xconn.ErrNoResult)
			}

			return xconn.NewInvocationResult("done")
		}).Do()
	require.NoError(t, registerResponse.Err)

	t.Run("ProgressiveCall", func(t *testing.T) {
		totalChunks := 6
		chunkIndex := 1

		callResponse := caller.Call("foo.bar.progress").
			ProgressSender(func(ctx context.Context) *xconn.Progress {
				defer func() { chunkIndex++ }()

				time.Sleep(10 * time.Millisecond)

				if chunkIndex == totalChunks-1 {
					return xconn.NewFinalProgress(chunkIndex)
				} else {
					return xconn.NewProgress(chunkIndex)
				}
			}).Do()
		require.NoError(t, callResponse.Err)

		// Verify progressive updates received correctly
		require.Equal(t, []int{1, 2, 3, 4, 5}, progressUpdates)

		// Verify the final result
		require.Equal(t, "done", callResponse.Args.StringOr(0, ""))
	})
}

func TestProgressiveCallInvocation(t *testing.T) {
	forEachSerializer(func(name string, serializerSpec xconn.SerializerSpec) {
		t.Run("NXTRouterWith"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, serializerSpec.Serializer())
			testProgressiveCallInvocation(t, callee, caller)
		})

		// msgpack is broken in nexus
		if name == msgPackSerializer {
			return
		}

		t.Run("NexusRouterWith"+name, func(t *testing.T) {
			session := connect(t, serializerSpec)
			testProgressiveCallInvocation(t, session, session)
		})
	})
}

func testCallProgressiveProgress(t *testing.T, callee, caller *xconn.Session) {
	// Store progress updates
	progressUpdates := make([]int, 0)
	registerResponse := callee.Register("foo.bar.progress",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			progress, _ := util.AsInt(invocation.Args()[0])
			progressUpdates = append(progressUpdates, progress)

			if invocation.Progress() {
				err := invocation.SendProgress([]any{progress}, nil)
				require.NoError(t, err)
				return xconn.NewInvocationError(xconn.ErrNoResult)
			}

			return xconn.NewInvocationResult(progress)
		}).Do()

	require.NoError(t, registerResponse.Err)

	t.Run("ProgressiveCall", func(t *testing.T) {
		receivedProgressBack := make([]int, 0)
		totalChunks := 6
		chunkIndex := 1

		callResponse := caller.Call("foo.bar.progress").
			ProgressSender(func(ctx context.Context) *xconn.Progress {
				defer func() { chunkIndex++ }()

				time.Sleep(10 * time.Millisecond)

				if chunkIndex == totalChunks-1 {
					return xconn.NewFinalProgress(chunkIndex)
				} else {
					return xconn.NewProgress(chunkIndex)
				}
			}).
			ProgressReceiver(func(result *xconn.InvocationResult) {
				progress, _ := util.AsInt(result.Args[0])
				receivedProgressBack = append(receivedProgressBack, progress)
			}).Do()

		require.NoError(t, callResponse.Err)

		finalResult, _ := util.AsInt(callResponse.Args.Raw()[0])
		receivedProgressBack = append(receivedProgressBack, finalResult)

		// Verify progressive updates received correctly
		require.Equal(t, []int{1, 2, 3, 4, 5}, progressUpdates)

		// Verify progressive updates mirrored correctly
		require.Equal(t, progressUpdates, receivedProgressBack)
	})
}

func TestCallProgressiveProgress(t *testing.T) {
	forEachSerializer(func(name string, serializerSpec xconn.SerializerSpec) {
		t.Run("NXTRouterWith"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, serializerSpec.Serializer())
			testCallProgressiveProgress(t, callee, caller)
		})

		// msgpack is broken in nexus
		if name == "msgpack" {
			return
		}

		t.Run("NexusRouterWith"+name, func(t *testing.T) {
			session := connect(t, serializerSpec)
			testCallProgressiveProgress(t, session, session)
		})
	})
}

func TestInMemorySession(t *testing.T) {
	forEachSerializer(func(name string, serializerSpec xconn.SerializerSpec) {
		t.Run(name, func(t *testing.T) {
			role := "trusted"
			procedure := "com.hello"

			router := xconn.NewRouter()
			err := router.AddRealm(realmName)
			require.NoError(t, err)

			session, err := xconn.ConnectInMemory(router, realmName)
			require.NoError(t, err)

			response := session.Register(
				procedure,
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					return xconn.NewInvocationResult("hello")
				},
			).Do()
			require.NoError(t, response.Err)

			cResponse := session.Call(procedure).Do()
			require.NoError(t, cResponse.Err)

			require.Equal(t, realmName, session.Details().Realm())
			require.Equal(t, role, session.Details().AuthRole())
		})
	})
}

func createUnixPath(t *testing.T) string {
	tmpFile, err := os.CreateTemp("", "unix-*.sock")
	require.NoError(t, err)
	require.NoError(t, os.Remove(tmpFile.Name()))
	return tmpFile.Name()
}

func TestUnixSocket(t *testing.T) {
	forEachSerializer(func(name string, serializerSpec xconn.SerializerSpec) {
		t.Run("UnixSocketWith"+name, func(t *testing.T) {
			router := xconn.NewRouter()
			err := router.AddRealm("unix")
			require.NoError(t, err)
			server := xconn.NewServer(router, nil, nil)

			t.Run("websocket-over-unix", func(t *testing.T) {
				tmpFile := createUnixPath(t)
				path := "unix+ws://" + tmpFile

				closer, err := server.ListenAndServeWebSocket("unix", tmpFile)
				require.NoError(t, err)
				t.Cleanup(func() { _ = closer.Close() })

				session, err := xconn.ConnectAnonymous(context.Background(), path, "unix")
				require.NoError(t, err)
				require.NotNil(t, session)
			})

			t.Run("rawsocket-over-unix", func(t *testing.T) {
				tmpFile := createUnixPath(t)
				closer, err := server.ListenAndServeRawSocket("unix", tmpFile)
				require.NoError(t, err)
				t.Cleanup(func() { _ = closer.Close() })

				path := "unix://" + tmpFile
				session, err := xconn.ConnectAnonymous(context.Background(), path, "unix")
				require.NoError(t, err)
				require.NotNil(t, session)

				path = "unix+rs://" + tmpFile
				session, err = xconn.ConnectAnonymous(context.Background(), path, "unix")
				require.NoError(t, err)
				require.NotNil(t, session)
			})
		})
	})
}

func TestAddRealm(t *testing.T) {
	router := xconn.NewRouter()
	err := router.AddRealm(realmName)
	require.NoError(t, err)

	// adding same realm twice should return error
	err = router.AddRealm(realmName)
	require.EqualError(t, err, fmt.Sprintf("realm '%s' already registered", realmName))
}

func TestPerformance(t *testing.T) {
	router := xconn.NewRouter()
	err := router.AddRealm("realm1")
	require.NoError(t, err)

	err = router.AddRealmRole("realm1", xconn.RealmRole{Name: "anonymous", Permissions: []xconn.Permission{{
		URI:            "",
		MatchPolicy:    "prefix",
		AllowCall:      true,
		AllowRegister:  true,
		AllowPublish:   true,
		AllowSubscribe: true,
	}}})
	require.NoError(t, err)

	server := xconn.NewServer(router, nil, nil)
	closer, err := server.ListenAndServeRawSocket("tcp", "127.0.0.1:9000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(rand.IntN(256))
	}

	callerClient := xconn.Client{
		SerializerSpec: xconn.CBORSerializerSpec,
	}

	caller, err := callerClient.Connect(context.Background(), "rs://localhost:9000", "realm1")
	require.NoError(t, err)
	require.NotNil(t, caller)

	calleeClient := xconn.Client{
		SerializerSpec: xconn.CBORSerializerSpec,
	}

	callee, err := calleeClient.Connect(context.Background(), "rs://localhost:9000", "realm1")
	require.NoError(t, err)
	require.NotNil(t, callee)

	response := callee.Register(
		"io.xconn.proto",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult(invocation.Args()...)
		},
	).Do()
	require.NoError(t, response.Err)

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			caller.Call("io.xconn.proto").Arg(data).Option("x_payload_raw", true).Do()
		}()
	}

	wg.Wait()
}
