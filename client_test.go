package xconn_test

import (
	"context"
	"errors"
	"fmt"
	"log"
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
		result, err := session.CallRaw(context.Background(), "foo.bar", nil, nil, nil)
		require.Error(t, err)
		require.Nil(t, result)

		var er *xconn.Error
		errors.As(err, &er)
		require.Equal(t, "wamp.error.no_such_procedure", er.URI)
	})
}

func TestRegisterCall(t *testing.T) {
	session := connect(t)
	request := xconn.NewRegisterRequest("foo.bar", func(ctx context.Context, invocation *xconn.Invocation) *xconn.Result {
		return &xconn.Result{Arguments: []any{"hello"}}
	})
	reg, err := session.Register(request)

	require.NoError(t, err)
	require.NotNil(t, reg)

	t.Run("CallRaw", func(t *testing.T) {
		wp := workerpool.New(10)
		for i := 0; i < 100; i++ {
			wp.Submit(func() {
				result, err := session.CallRaw(context.Background(), "foo.bar", nil, nil, nil)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "hello", result.Arguments[0])
			})
		}

		wp.StopWait()
	})
}

func TestPublishSubscribe(t *testing.T) {
	session := connect(t)
	event1 := make(chan *xconn.Event, 1)
	request := xconn.NewSubscribeRequest("foo.bar",
		func(event *xconn.Event) {
			event1 <- event
		})
	reg, err := session.Subscribe(request)

	require.NoError(t, err)
	require.NotNil(t, reg)

	t.Run("Publish", func(t *testing.T) {
		opt := map[string]any{
			"exclude_me": false,
		}
		err := session.PublishRaw("foo.bar", nil, nil, opt)
		require.NoError(t, err)

		event := <-event1
		log.Println(event)
	})
}

func TestProgressiveCallResults(t *testing.T) {
	session := connect(t)

	request := xconn.NewRegisterRequest("foo.bar.progress",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.Result {
			// Send progress
			for i := 1; i <= 3; i++ {
				err := invocation.SendProgress([]any{i}, nil)
				require.NoError(t, err)
			}

			// Return final result
			return &xconn.Result{Arguments: []any{"done"}}
		})
	reg, err := session.Register(request)
	require.NoError(t, err)
	require.NotNil(t, reg)

	t.Run("ProgressiveCall", func(t *testing.T) {
		// Store received progress updates
		progressUpdates := make([]int, 0)

		callRequest := xconn.NewCallRequest("foo.bar.progress").ProgressReceiver(func(progressiveResult *xconn.Result) {
			progress := int(progressiveResult.Arguments[0].(float64))
			// Collect received progress
			progressUpdates = append(progressUpdates, progress)
		})
		result, err := session.Call(context.Background(), callRequest)
		require.NoError(t, err)

		// Verify progressive updates received correctly
		require.Equal(t, []int{1, 2, 3}, progressUpdates)

		// Verify the final result
		require.Equal(t, "done", result.Arguments[0])
	})
}

func TestProgressiveCallInvocation(t *testing.T) {
	session := connect(t)

	// Store progress updates
	progressUpdates := make([]int, 0)
	request := xconn.NewRegisterRequest("foo.bar.progress",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.Result {
			progress := int(invocation.Arguments[0].(float64))
			progressUpdates = append(progressUpdates, progress)

			isProgress, _ := invocation.Details[wampproto.OptionProgress].(bool)
			if isProgress {
				return &xconn.Result{Err: xconn.ErrNoResult}
			}

			return &xconn.Result{Arguments: []any{"done"}}
		})
	reg, err := session.Register(request)
	require.NoError(t, err)
	require.NotNil(t, reg)

	t.Run("ProgressiveCall", func(t *testing.T) {
		totalChunks := 6
		chunkIndex := 1

		callRequest := xconn.NewCallRequest("foo.bar.progress").
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
				return &xconn.Progress{Arguments: args, Options: options}
			})
		result, err := session.Call(context.Background(), callRequest)
		require.NoError(t, err)

		// Verify progressive updates received correctly
		require.Equal(t, []int{1, 2, 3, 4, 5}, progressUpdates)

		// Verify the final result
		require.Equal(t, "done", result.Arguments[0])
	})
}

func TestCallProgressiveProgress(t *testing.T) {
	session := connect(t)

	// Store progress updates
	progressUpdates := make([]int, 0)
	request := xconn.NewRegisterRequest("foo.bar.progress",
		func(ctx context.Context, invocation *xconn.Invocation) *xconn.Result {
			progress := int(invocation.Arguments[0].(float64))
			progressUpdates = append(progressUpdates, progress)

			isProgress, _ := invocation.Details[wampproto.OptionProgress].(bool)
			if isProgress {
				err := invocation.SendProgress([]any{progress}, nil)
				require.NoError(t, err)
				return &xconn.Result{Err: xconn.ErrNoResult}
			}

			return &xconn.Result{Arguments: []any{progress}}
		})
	reg, err := session.Register(request)
	require.NoError(t, err)
	require.NotNil(t, reg)

	t.Run("ProgressiveCall", func(t *testing.T) {
		receivedProgressBack := make([]int, 0)
		totalChunks := 6
		chunkIndex := 1

		callRequest := xconn.NewCallRequest("foo.bar.progress").
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
				return &xconn.Progress{Arguments: args, Options: options}
			}).
			ProgressReceiver(func(result *xconn.Result) {
				progress := int(result.Arguments[0].(float64))
				receivedProgressBack = append(receivedProgressBack, progress)
			})

		result, err := session.Call(context.Background(), callRequest)
		require.NoError(t, err)

		finalResult := int(result.Arguments[0].(float64))
		receivedProgressBack = append(receivedProgressBack, finalResult)

		// Verify progressive updates received correctly
		require.Equal(t, []int{1, 2, 3, 4, 5}, progressUpdates)

		// Verify progressive updates mirrored correctly
		require.Equal(t, progressUpdates, receivedProgressBack)
	})
}
