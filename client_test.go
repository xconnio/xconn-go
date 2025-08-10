package xconn_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"testing"
	"time"

	"github.com/gammazero/workerpool"
	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go"
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
		wp := workerpool.New(10)
		for i := 0; i < 100; i++ {
			wp.Submit(func() {
				callResponse := session.Call("foo.bar").Do()
				require.NoError(t, callResponse.Err)
				require.NotNil(t, callResponse)
				require.Equal(t, "hello", callResponse.Args[0])
			})
		}

		wp.StopWait()
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
		require.Equal(t, "done", callResponse.Args[0])
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

			isProgress, _ := invocation.Details()[wampproto.OptionProgress].(bool)
			if isProgress {
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
				options := map[string]any{}

				// Mark the last chunk as non-progressive
				if chunkIndex == totalChunks-1 {
					options[wampproto.OptionProgress] = false
				} else {
					options[wampproto.OptionProgress] = true
				}

				args := []any{chunkIndex}
				chunkIndex++

				time.Sleep(10 * time.Millisecond)
				return &xconn.Progress{Args: args, Options: options}
			}).Do()
		require.NoError(t, callResponse.Err)

		// Verify progressive updates received correctly
		require.Equal(t, []int{1, 2, 3, 4, 5}, progressUpdates)

		// Verify the final result
		require.Equal(t, "done", callResponse.Args[0])
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

			isProgress, _ := invocation.Details()[wampproto.OptionProgress].(bool)
			if isProgress {
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
				options := map[string]any{}

				// Mark the last chunk as non-progressive
				if chunkIndex == totalChunks-1 {
					options[wampproto.OptionProgress] = false
				} else {
					options[wampproto.OptionProgress] = true
				}

				args := []any{chunkIndex}
				chunkIndex++

				time.Sleep(10 * time.Millisecond)
				return &xconn.Progress{Args: args, Options: options}
			}).
			ProgressReceiver(func(result *xconn.InvocationResult) {
				progress := int(result.Args[0].(float64))
				receivedProgressBack = append(receivedProgressBack, progress)
			}).Do()

		require.NoError(t, callResponse.Err)

		finalResult := int(callResponse.Args[0].(float64))
		receivedProgressBack = append(receivedProgressBack, finalResult)

		// Verify progressive updates received correctly
		require.Equal(t, []int{1, 2, 3, 4, 5}, progressUpdates)

		// Verify progressive updates mirrored correctly
		require.Equal(t, progressUpdates, receivedProgressBack)
	})
}

func TestInMemorySession(t *testing.T) {
	realmName := "realm1"
	role := "trusted"
	procedure := "com.hello"

	router := xconn.NewRouter()
	router.AddRealm(realmName)

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
	router.AddRealm("unix")
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
