package xconn_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"

	"github.com/gammazero/workerpool"
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
		result, err := session.Call(context.Background(), "foo.bar", nil, nil, nil)
		require.Error(t, err)
		require.Nil(t, result)

		var er *xconn.Error
		errors.As(err, &er)
		require.Equal(t, "wamp.error.no_such_procedure", er.URI)
	})
}

func TestRegisterCall(t *testing.T) {
	session := connect(t)
	reg, err := session.Register(
		context.Background(),
		"foo.bar",
		func(ctx context.Context, invocation *xconn.Invocation) (*xconn.Result, error) {
			return &xconn.Result{Args: []any{"hello"}}, nil
		},
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, reg)

	t.Run("Call", func(t *testing.T) {
		wp := workerpool.New(10)
		for i := 0; i < 100; i++ {
			wp.Submit(func() {
				result, err := session.Call(context.Background(), "foo.bar", nil, nil, nil)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "hello", result.Args[0])
			})
		}

		wp.StopWait()
	})
}

func TestPublishSubscribe(t *testing.T) {
	session := connect(t)
	event1 := make(chan *xconn.Event, 1)
	reg, err := session.Subscribe(
		context.Background(),
		"foo.bar",
		func(event *xconn.Event) {
			event1 <- event
		},
		nil,
	)

	require.NoError(t, err)
	require.NotNil(t, reg)

	t.Run("Publish", func(t *testing.T) {
		opt := map[string]any{
			"exclude_me": false,
		}
		err := session.Publish(context.Background(), "foo.bar", nil, nil, opt)
		require.NoError(t, err)

		event := <-event1
		log.Println(event)
	})
}
