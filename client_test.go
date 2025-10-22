package xconn_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func connect(t *testing.T) *xconn.Session {
	listener := startRouter(t, "realm1")
	defer func() { _ = listener.Close() }()
	address := fmt.Sprintf("ws://%s/ws", listener.Addr().String())

	client := &xconn.Client{
		SerializerSpec: xconn.JSONSerializerSpec,
	}

	session, err := client.Connect(context.Background(), address, "realm1")
	require.NoError(t, err)
	require.NotNil(t, session)

	return session
}

func TestCall(t *testing.T) {
	session := connect(t)
	t.Run("CallNoProc", func(t *testing.T) {
		callResponse := session.Call("foo.bar").Do()
		require.Error(t, callResponse.Err)

		var er *xconn.Error
		errors.As(callResponse.Err, &er)
		require.Equal(t, "wamp.error.no_such_procedure", er.URI)
	})
}

func TestRegisterCall(t *testing.T) {
	session := connect(t)
	registerResponse := session.Register("foo.bar",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			return xconn.NewInvocationResult("hello")
		}).Do()

	require.NoError(t, registerResponse.Err)

	t.Run("callRaw", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				callResponse := session.Call("foo.bar").Do()
				require.NoError(t, callResponse.Err)
				require.NotNil(t, callResponse)
				require.Equal(t, "hello", callResponse.Args.StringOr(0, ""))
			}()
		}
		wg.Wait()
	})
}

func TestPublishSubscribe(t *testing.T) {
	session := connect(t)
	event1 := make(chan *xconn.Event, 1)
	subscribeResponse := session.Subscribe(
		"foo.bar",
		func(event *xconn.Event) {
			event1 <- event
		}).Do()

	require.NoError(t, subscribeResponse.Err)

	t.Run("PublishWithRequest", func(t *testing.T) {
		opt := map[string]any{
			"exclude_me": false,
		}
		publishResponse := session.Publish("foo.bar").Options(opt).Do()
		require.NoError(t, publishResponse.Err)

		event := <-event1
		log.Println(event)
	})
}

func TestProgressiveCallResults(t *testing.T) {
	session := connect(t)

	registerResponse := session.Register("foo.bar.progress",
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

		callResponse := session.Call("foo.bar.progress").ProgressReceiver(func(progressiveResult *xconn.InvocationResult) {
			progress := int(progressiveResult.Args[0].(float64))
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

func TestProgressiveCallInvocation(t *testing.T) {
	session := connect(t)

	// Store progress updates
	progressUpdates := make([]int, 0)
	registerResponse := session.Register("foo.bar.progress",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			progress, _ := invocation.ArgFloat64(0)
			progressUpdates = append(progressUpdates, int(progress))
			if invocation.Progress() {
				return xconn.NewInvocationError(xconn.ErrNoResult)
			}

			return xconn.NewInvocationResult("done")
		}).Do()
	require.NoError(t, registerResponse.Err)

	t.Run("ProgressiveCall", func(t *testing.T) {
		totalChunks := 6
		chunkIndex := 1

		callResponse := session.Call("foo.bar.progress").
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

func TestCallProgressiveProgress(t *testing.T) {
	session := connect(t)

	// Store progress updates
	progressUpdates := make([]int, 0)
	registerResponse := session.Register("foo.bar.progress",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
			progress, _ := invocation.ArgFloat64(0)
			progressUpdates = append(progressUpdates, int(progress))

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

		callResponse := session.Call("foo.bar.progress").
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
				progress := int(result.Args[0].(float64))
				receivedProgressBack = append(receivedProgressBack, progress)
			}).Do()

		require.NoError(t, callResponse.Err)

		finalResult := int(callResponse.Args.Float64Or(0, 1))
		receivedProgressBack = append(receivedProgressBack, finalResult)

		// Verify progressive updates received correctly
		require.Equal(t, []int{1, 2, 3, 4, 5}, progressUpdates)

		// Verify progressive updates mirrored correctly
		require.Equal(t, progressUpdates, receivedProgressBack)
	})
}

func TestInMemorySession(t *testing.T) {
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
}

func createUnixPath(t *testing.T) string {
	tmpFile, err := os.CreateTemp("", "unix-*.sock")
	require.NoError(t, err)
	require.NoError(t, os.Remove(tmpFile.Name()))
	return tmpFile.Name()
}

func TestUnixSocket(t *testing.T) {
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

	server := xconn.NewServer(router, nil, nil)
	closer, err := server.ListenAndServeRawSocket("tcp", "127.0.0.1:9000")
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	data := make([]byte, 1024*1024)
	_, err = rand.Read(data)
	require.NoError(t, err)

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
	require.NotNil(t, caller)

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
