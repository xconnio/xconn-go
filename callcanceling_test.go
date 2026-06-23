package xconn_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/xconn-go"
)

// TestCallCancelingCallerContextTimeout tests that when the caller's context expires,
// a CANCEL is sent to the router, the callee's context is cancelled, and the caller
// receives wamp.error.canceled.
func TestCallCancelingCallerContextTimeout(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, spec.Serializer())

			calleeDone := make(chan struct{})
			regResp := callee.Register("io.xconn.slow",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					defer close(calleeDone)
					select {
					case <-ctx.Done():
						return xconn.NewInvocationError(wampproto.ErrCanceled)
					case <-time.After(10 * time.Second):
						return xconn.NewInvocationResult("done")
					}
				}).Do()
			require.NoError(t, regResp.Err)

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			resp := caller.Call("io.xconn.slow").DoContext(ctx)
			require.Error(t, resp.Err)

			var er *xconn.Error
			require.True(t, errors.As(resp.Err, &er))
			require.Equal(t, wampproto.ErrCanceled, er.URI)

			select {
			case <-calleeDone:
			case <-time.After(time.Second):
				t.Fatal("callee did not receive context cancellation")
			}
		})
	})
}

// TestCallCanceling_CallerCancelFunc tests cancellation via an explicit cancel function.
func TestCallCancelingCallerCancelFunc(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, spec.Serializer())

			invocationStarted := make(chan struct{})
			regResp := callee.Register("io.xconn.slow",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					close(invocationStarted)
					select {
					case <-ctx.Done():
						return xconn.NewInvocationError(wampproto.ErrCanceled)
					case <-time.After(10 * time.Second):
						return xconn.NewInvocationResult("done")
					}
				}).Do()
			require.NoError(t, regResp.Err)

			ctx, cancel := context.WithCancel(context.Background())

			resultCh := make(chan xconn.CallResponse, 1)
			go func() { resultCh <- caller.Call("io.xconn.slow").DoContext(ctx) }()

			// wait for callee to start before cancelling
			select {
			case <-invocationStarted:
			case <-time.After(time.Second):
				t.Fatal("callee never started")
			}

			cancel()

			resp := <-resultCh
			require.Error(t, resp.Err)

			var er *xconn.Error
			require.True(t, errors.As(resp.Err, &er), "expected *xconn.Error, got: %v", resp.Err)
			require.Equal(t, wampproto.ErrCanceled, er.URI)
		})
	})
}

// TestCallCancelingKillNoWait tests that with killnowait (default), the caller receives
// the canceled error immediately even when the callee ignores the interrupt and keeps running.
func TestCallCancelingKillNoWait(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, spec.Serializer())

			calleeResponseCh := make(chan struct{}, 1)
			regResp := callee.Register("io.xconn.stubborn",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					time.Sleep(200 * time.Millisecond)
					calleeResponseCh <- struct{}{}
					return xconn.NewInvocationResult("done")
				}).Do()
			require.NoError(t, regResp.Err)

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			start := time.Now()
			resp := caller.Call("io.xconn.stubborn").DoContext(ctx)
			elapsed := time.Since(start)

			// caller should get back quickly (not after the full 200ms callee sleep)
			require.Less(t, elapsed, 150*time.Millisecond,
				"caller should receive canceled error quickly in killnowait mode")
			require.Error(t, resp.Err)

			var er *xconn.Error
			require.True(t, errors.As(resp.Err, &er), "expected *xconn.Error, got: %v", resp.Err)
			require.Equal(t, wampproto.ErrCanceled, er.URI)
		})
	})
}

// TestCallCancelingCalleeContextCancelled verifies that the callee's context is cancelled
// when an INTERRUPT is received from the dealer.
func TestCallCancelingCalleeContextCancelled(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, spec.Serializer())

			ctxCancelledCh := make(chan struct{})
			regResp := callee.Register("io.xconn.waitcancel",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					<-ctx.Done()
					close(ctxCancelledCh)
					return xconn.NewInvocationError(wampproto.ErrCanceled)
				}).Do()
			require.NoError(t, regResp.Err)

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			resp := caller.Call("io.xconn.waitcancel").DoContext(ctx)
			require.Error(t, resp.Err)

			select {
			case <-ctxCancelledCh:
			case <-time.After(time.Second):
				t.Fatal("callee context was not cancelled after INTERRUPT")
			}
		})
	})
}

// TestCallCancelingProgressiveSenderAbort tests that when a progressive call sender
// returns an error mid-stream, a CANCEL is sent and the caller receives wamp.error.canceled.
func TestCallCancelingProgressiveSenderAbort(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, spec.Serializer())

			regResp := callee.Register("io.xconn.progressive",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					if invocation.Progress() {
						return xconn.NewInvocationError(xconn.ErrNoResult)
					}
					return xconn.NewInvocationResult("done")
				}).Do()
			require.NoError(t, regResp.Err)

			chunkIndex := 0
			resp := caller.Call("io.xconn.progressive").
				ProgressSender(func(ctx context.Context) *xconn.Progress {
					chunkIndex++
					if chunkIndex == 1 {
						return xconn.NewProgress(chunkIndex)
					}
					// abort on second chunk
					return &xconn.Progress{Err: fmt.Errorf("sender aborted")}
				}).Do()

			require.Error(t, resp.Err)
			var er *xconn.Error
			require.True(t, errors.As(resp.Err, &er))
			require.Equal(t, wampproto.ErrCanceled, er.URI)
		})
	})
}

// TestCallCancelingProgressiveResults tests that canceling a call while the callee is
// streaming progress results stops the stream and the caller gets wamp.error.canceled.
func TestCallCancelingProgressiveResults(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, spec.Serializer())

			calleeDone := make(chan struct{})
			regResp := callee.Register("io.xconn.stream",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					defer close(calleeDone)
					for i := 0; ; i++ {
						if err := ctx.Err(); err != nil {
							return xconn.NewInvocationError(wampproto.ErrCanceled)
						}
						if err := invocation.SendProgress([]any{i}, nil); err != nil {
							return xconn.NewInvocationError(wampproto.ErrCanceled)
						}
						time.Sleep(20 * time.Millisecond)
					}
				}).Do()
			require.NoError(t, regResp.Err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			chunksReceived := 0
			resultCh := make(chan xconn.CallResponse, 1)
			go func() {
				resultCh <- caller.Call("io.xconn.stream").
					ProgressReceiver(func(_ *xconn.ProgressResult) {
						chunksReceived++
						if chunksReceived == 2 {
							cancel()
						}
					}).DoContext(ctx)
			}()

			resp := <-resultCh
			require.Error(t, resp.Err)
			var er *xconn.Error
			require.True(t, errors.As(resp.Err, &er))
			require.Equal(t, wampproto.ErrCanceled, er.URI)
			require.GreaterOrEqual(t, chunksReceived, 2)

			select {
			case <-calleeDone:
			case <-time.After(2 * time.Second):
				t.Fatal("callee did not stop streaming after cancel")
			}
		})
	})
}

// TestCallCancelingCallerDisconnect tests that when the caller disconnects mid-call,
// the dealer sends a lazy INTERRUPT to the callee, cancelling its context.
func TestCallCancelingCallerDisconnect(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			router, err := xconn.NewRouter(nil)
			require.NoError(t, err)
			require.NoError(t, router.AddRealm(realmName, xconn.DefaultRealmConfig()))

			callee := connectInMemory(t, router, spec.Serializer())
			caller := connectInMemory(t, router, spec.Serializer())

			calleeDone := make(chan struct{})
			callerReceivedProgress := make(chan struct{}, 1)
			regResp := callee.Register("io.xconn.stream",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					defer close(calleeDone)
					for i := 0; ; i++ {
						if err := ctx.Err(); err != nil {
							return xconn.NewInvocationError(wampproto.ErrCanceled)
						}
						if err := invocation.SendProgress([]any{i}, nil); err != nil {
							return xconn.NewInvocationError(wampproto.ErrCanceled)
						}
						time.Sleep(20 * time.Millisecond)
					}
				}).Do()
			require.NoError(t, regResp.Err)

			go func() {
				caller.Call("io.xconn.stream").
					ProgressReceiver(func(_ *xconn.ProgressResult) {
						select {
						case callerReceivedProgress <- struct{}{}:
						default:
						}
					}).Do()
			}()

			// wait until the callee is streaming, then disconnect the caller
			select {
			case <-callerReceivedProgress:
			case <-time.After(time.Second):
				t.Fatal("callee never started streaming")
			}
			require.NoError(t, caller.Leave())

			select {
			case <-calleeDone:
			case <-time.After(2 * time.Second):
				t.Fatal("callee context was not cancelled after caller disconnected")
			}
		})
	})
}

// TestCallCancelingConcurrentCallsIsolated verifies that cancelling one call does not
// affect other concurrent calls to the same procedure.
func TestCallCancelingConcurrentCallsIsolated(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			router, err := xconn.NewRouter(nil)
			require.NoError(t, err)
			require.NoError(t, router.AddRealm(realmName, xconn.DefaultRealmConfig()))

			callee := connectInMemory(t, router, spec.Serializer())
			caller1 := connectInMemory(t, router, spec.Serializer())
			caller2 := connectInMemory(t, router, spec.Serializer())

			regResp := callee.Register("io.xconn.maybe_slow",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					slow, _ := invocation.Kwargs()["slow"].(bool)
					if slow {
						select {
						case <-ctx.Done():
							return xconn.NewInvocationError(wampproto.ErrCanceled)
						case <-time.After(10 * time.Second):
							return xconn.NewInvocationResult("slow")
						}
					}
					return xconn.NewInvocationResult("fast")
				}).Do()
			require.NoError(t, regResp.Err)

			// caller1: cancelled after short timeout
			cancelledCh := make(chan xconn.CallResponse, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
				cancelledCh <- caller1.Call("io.xconn.maybe_slow").Kwarg("slow", true).DoContext(ctx)
			}()

			// caller2: normal call, should complete regardless of caller1's cancel
			okCh := make(chan xconn.CallResponse, 1)
			go func() {
				okCh <- caller2.Call("io.xconn.maybe_slow").Do()
			}()

			cancelledResp := <-cancelledCh
			require.Error(t, cancelledResp.Err)
			var er *xconn.Error
			require.True(t, errors.As(cancelledResp.Err, &er))
			require.Equal(t, wampproto.ErrCanceled, er.URI)

			okResp := <-okCh
			require.NoError(t, okResp.Err)
			require.Equal(t, "fast", okResp.ArgStringOr(0, ""))
		})
	})
}

// TestCallCancelingSubsequentCallsWork verifies that after a cancelled call, the same
// procedure can still be called successfully.
func TestCallCancelingSubsequentCallsWork(t *testing.T) {
	forEachSerializer(func(name string, spec xconn.SerializerSpec) {
		t.Run("With"+name, func(t *testing.T) {
			callee, caller := connectedLocalSessions(t, spec.Serializer())

			regResp := callee.Register("io.xconn.maybe_slow",
				func(ctx context.Context, invocation *xconn.Invocation) *xconn.InvocationResult {
					slow, _ := invocation.Kwargs()["slow"].(bool)
					if slow {
						select {
						case <-ctx.Done():
							return xconn.NewInvocationError(wampproto.ErrCanceled)
						case <-time.After(10 * time.Second):
							return xconn.NewInvocationResult("slow")
						}
					}
					return xconn.NewInvocationResult("fast")
				}).Do()
			require.NoError(t, regResp.Err)

			// first call: cancelled
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			cancelledResp := caller.Call("io.xconn.maybe_slow").Kwarg("slow", true).DoContext(ctx)
			require.Error(t, cancelledResp.Err)

			var er *xconn.Error
			require.True(t, errors.As(cancelledResp.Err, &er))
			require.Equal(t, wampproto.ErrCanceled, er.URI)

			// subsequent call: should succeed normally
			okResp := caller.Call("io.xconn.maybe_slow").Do()
			require.NoError(t, okResp.Err)
			require.Equal(t, "fast", okResp.ArgStringOr(0, ""))
		})
	})
}
