package xconn

import (
	"context"

	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/xconn-go"
)

// bridgeHandler forwards an invocation into the same-named procedure on appSession,
// chaining progressive results through so streaming procedures still work end to end.
func bridgeHandler(appSession *xconn.Session, procedure string) xconn.InvocationHandler {
	return func(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
		call := appSession.Call(procedure).Args(inv.Args()...).Kwargs(inv.Kwargs())
		if receiveProgress, _ := inv.Details()[wampproto.OptionReceiveProgress].(bool); receiveProgress {
			call = call.ProgressReceiver(func(r *xconn.ProgressResult) {
				_ = inv.SendProgress(r.Args(), r.Kwargs())
			})
		}

		resp := call.Do()
		if resp.Err != nil {
			return xconn.NewInvocationError(errOperationFailed, resp.Err.Error())
		}
		return xconn.NewInvocationResult(resp.Args()...)
	}
}

// RegisterBridge registers a forwarding handler on session for each of app's procedures,
// relaying every call into app.Session.
func RegisterBridge(session *xconn.Session, app *App) error {
	for _, procedure := range app.Procedures {
		resp := session.Register(procedure, bridgeHandler(app.Session, procedure)).Invoke(wampproto.InvokeLast).Do()
		if resp.Err != nil {
			return resp.Err
		}
	}
	return nil
}
