package xconn_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func startRouterWithManagementAPIs(t *testing.T) *xconn.Session {
	r := xconn.NewRouter()
	err := r.AddRealm(realmName)
	require.NoError(t, err)
	inMemorySession, err := xconn.ConnectInMemory(r, realmName)
	require.NoError(t, err)
	require.NoError(t, r.EnableManagementAPI(inMemorySession))

	callerSession, err := xconn.ConnectInMemory(r, realmName)
	require.NoError(t, err)
	return callerSession
}

func TestManagementStatsAPIs(t *testing.T) {
	session := startRouterWithManagementAPIs(t)

	eventChannel := make(chan *xconn.Event)
	subResp := session.Subscribe(xconn.ManagementTopicStats, func(event *xconn.Event) {
		eventChannel <- event
	}).Do()
	require.NoError(t, subResp.Err)

	callResp := session.Call(xconn.ManagementProcedureEnableStats).Arg(100).Do()
	require.NoError(t, callResp.Err)

	require.Eventually(t, func() bool {
		<-eventChannel
		return true
	}, 2*time.Second, 50*time.Millisecond)

	callResp = session.Call(xconn.ManagementProcedureStatsStatus).Do()
	require.NoError(t, callResp.Err)
	require.Equal(t, map[string]any{"interval": int64(100), "running": true}, callResp.ArgDictOr(0, map[string]any{}))

	callResp = session.Call(xconn.ManagementProcedureDisableStats).Arg(100).Do()
	require.NoError(t, callResp.Err)
	require.NoError(t, callResp.Err)

	callResp = session.Call(xconn.ManagementProcedureStatsStatus).Do()
	require.NoError(t, callResp.Err)
	require.Equal(t, map[string]any{"interval": int64(100), "running": false}, callResp.ArgDictOr(0, map[string]any{}))
}
