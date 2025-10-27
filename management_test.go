package xconn_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func startRouterWithManagementAPIs(t *testing.T) (*xconn.Router, *xconn.Session) {
	r := xconn.NewRouter()
	err := r.AddRealm(realmName)
	require.NoError(t, err)
	require.NoError(t, r.AddRealm(xconn.ManagementRealm))
	require.NoError(t, r.EnableManagementAPI())

	callerSession, err := xconn.ConnectInMemory(r, xconn.ManagementRealm)
	require.NoError(t, err)
	return r, callerSession
}

func TestManagementStatsAPIs(t *testing.T) {
	_, session := startRouterWithManagementAPIs(t)

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

	callResp = session.Call(xconn.ManagementProcedureStatsStatusGet).Do()
	require.NoError(t, callResp.Err)
	require.Equal(t, map[string]any{"interval": int64(100), "running": true}, callResp.ArgDictOr(0, map[string]any{}))

	callResp = session.Call(xconn.ManagementProcedureDisableStats).Arg(100).Do()
	require.NoError(t, callResp.Err)
	require.NoError(t, callResp.Err)

	callResp = session.Call(xconn.ManagementProcedureStatsStatusGet).Do()
	require.NoError(t, callResp.Err)
	require.Equal(t, map[string]any{"interval": int64(100), "running": false}, callResp.ArgDictOr(0, map[string]any{}))
}

func TestManagementRealmListApi(t *testing.T) {
	_, session := startRouterWithManagementAPIs(t)

	callResp := session.Call(xconn.ManagementProcedureListRealms).Do()
	require.NoError(t, callResp.Err)
	require.ElementsMatch(t, []any{"io.xconn.mgmt", "test"}, callResp.ArgListOr(0, []any{}))
}

func TestManagementSessionList(t *testing.T) {
	r, session := startRouterWithManagementAPIs(t)

	callResp := session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []any{}), 2)

	for i := 0; i < 20; i++ {
		_, err := xconn.ConnectInMemory(r, xconn.ManagementRealm)
		require.NoError(t, err)
	}

	callResp = session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []any{}), 22)

	callResp = session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Kwargs(map[string]any{
		"limit":  10,
		"offset": 0,
	}).Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []any{}), 10)

	callResp = session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Kwargs(map[string]any{
		"limit":  10,
		"offset": 10,
	}).Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []any{}), 10)

	callResp = session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Kwargs(map[string]any{
		"limit":  10,
		"offset": 20,
	}).Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []any{}), 2)
}
