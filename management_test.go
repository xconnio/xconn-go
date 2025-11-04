package xconn_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/xconn-go"
)

func startRouterWithManagementAPIs(t *testing.T) (*xconn.Router, *xconn.Session) {
	r, err := xconn.NewRouter(&xconn.RouterConfig{Management: true})
	require.NoError(t, err)
	err = r.AddRealm(realmName, xconn.DefaultRealmConfig())
	require.NoError(t, err)

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

	callResp := session.Call(xconn.ManagementProcedureStatsStatusSet).Kwargs(map[string]any{
		"interval": 100,
		"enable":   true,
	}).Do()
	require.NoError(t, callResp.Err)

	require.Eventually(t, func() bool {
		<-eventChannel
		return true
	}, 2*time.Second, 50*time.Millisecond)

	callResp = session.Call(xconn.ManagementProcedureStatsStatusGet).Do()
	require.NoError(t, callResp.Err)
	require.Equal(t, map[string]any{"interval": int64(100), "running": true}, callResp.ArgDictOr(0, xconn.Dict{}).Raw())

	callResp = session.Call(xconn.ManagementProcedureStatsStatusSet).Kwarg("disable", true).Do()
	require.NoError(t, callResp.Err)
	require.NoError(t, callResp.Err)

	callResp = session.Call(xconn.ManagementProcedureStatsStatusGet).Do()
	require.NoError(t, callResp.Err)
	require.Equal(t, map[string]any{"interval": int64(100), "running": false}, callResp.ArgDictOr(0, xconn.Dict{}).Raw())

	callResp = session.Call(xconn.ManagementProcedureStatsGet).Do()
	require.NoError(t, callResp.Err)
	_, err := callResp.ArgDict(0)
	require.NoError(t, err)
}

func TestManagementRealmListApi(t *testing.T) {
	_, session := startRouterWithManagementAPIs(t)

	callResp := session.Call(xconn.ManagementProcedureListRealms).Do()
	require.NoError(t, callResp.Err)
	require.ElementsMatch(t, []any{"io.xconn.mgmt", "test"}, callResp.ArgListOr(0, []xconn.Value{}).Raw())
}

func TestManagementSessionList(t *testing.T) {
	r, session := startRouterWithManagementAPIs(t)

	callResp := session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []xconn.Value{}), 2)

	for i := 0; i < 20; i++ {
		_, err := xconn.ConnectInMemory(r, xconn.ManagementRealm)
		require.NoError(t, err)
	}

	callResp = session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []xconn.Value{}), 22)

	callResp = session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Kwargs(map[string]any{
		"limit":  10,
		"offset": 0,
	}).Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []xconn.Value{}), 10)

	callResp = session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Kwargs(map[string]any{
		"limit":  10,
		"offset": 10,
	}).Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []xconn.Value{}), 10)

	callResp = session.Call(xconn.ManagementProcedureListSession).Arg("io.xconn.mgmt").Kwargs(map[string]any{
		"limit":  10,
		"offset": 20,
	}).Do()
	require.NoError(t, callResp.Err)
	require.Len(t, callResp.ArgListOr(0, []xconn.Value{}), 2)
}

func TestManagementSessionKill(t *testing.T) {
	r, session := startRouterWithManagementAPIs(t)
	require.NoError(t, r.AddRealm("realm1", &xconn.RealmConfig{}))

	sessionToKill, err := xconn.ConnectInMemory(r, "realm1")
	require.NoError(t, err)

	response := session.Call(xconn.ManagementProcedureKillSession).Args("realm1", sessionToKill.ID()).Do()
	require.NoError(t, response.Err)

	require.Eventually(t, func() bool {
		return !sessionToKill.Connected()
	}, 1*time.Second, 50*time.Millisecond)
}

func TestManagementSessionLog(t *testing.T) {
	r, session := startRouterWithManagementAPIs(t)
	require.NoError(t, r.AddRealm("realm1", &xconn.RealmConfig{}))

	sessionToLog, err := xconn.ConnectInMemory(r, "realm1")
	require.NoError(t, err)

	eventChannel := make(chan *xconn.Event, 1)
	subResp := session.Subscribe(
		fmt.Sprintf(xconn.ManagementTopicSessionLogTemplate, sessionToLog.ID()),
		func(event *xconn.Event) {
			eventChannel <- event
		},
	).Do()
	require.NoError(t, subResp.Err)

	// Enable session log
	response := session.Call(xconn.ManagementProcedureSessionLogSet).Args("realm1", sessionToLog.ID()).
		Kwarg("enable", true).Do()
	require.NoError(t, response.Err)

	// Should trigger event
	sessionToLog.Publish("test").Do()
	require.Eventually(t, func() bool {
		<-eventChannel
		return true
	}, 2*time.Second, 50*time.Millisecond)

	// Disable session log
	response = session.Call(xconn.ManagementProcedureSessionLogSet).Args("realm1", sessionToLog.ID()).
		Kwarg("enable", false).Do()
	require.NoError(t, response.Err)

	// Publish again should not trigger event
	sessionToLog.Publish("test").Do()
	select {
	case <-eventChannel:
		t.Fatal("unexpected event received after disabling session log")
	case <-time.After(500 * time.Millisecond):
	}

	// Call with no kwargs
	response = session.Call(xconn.ManagementProcedureSessionLogSet).Args("realm1", sessionToLog.ID()).Do()
	require.ErrorContains(t, response.Err, "wamp.error.invalid_argument")
}
