package xconn

import (
	"github.com/xconnio/wampproto-go"
	"github.com/xconnio/xconn-go"
)

const (
	xconnURIPrefix  = "io.xconn."
	webrtcURIPrefix = "io.xconn.webrtc."

	// WebRTC signaling on the device realm.
	ProcedureWebRTCOffer     = "io.xconn.webrtc.offer"
	TopicAnswererOnCandidate = "io.xconn.webrtc.answerer.on_candidate"
	TopicOffererOnCandidate  = "io.xconn.webrtc.offerer.on_candidate"

	// DefaultSTUNServer is the STUN server used when no ICE servers are configured.
	DefaultSTUNServer = "stun:stun.l.google.com:19302"

	errOperationFailed = "wamp.error.operation_failed"

	networkUnix = "unix"
)

// App is the local application xconn exposes to remote clients.
type App struct {
	// Session is a session on the app layer (see StartAppLayer) that bridged calls are
	// forwarded through.
	Session *xconn.Session
	// Procedures are the app-layer procedures remote clients may call.
	Procedures []string
	// StreamSocket is the Unix socket the app listens on for relayed raw streams and data
	// channels (see RelayHeader). Empty disables relaying.
	StreamSocket string
}

// StartAppLayer serves realm on the Unix socket at socketPath: applications dial in there
// to register their procedures, and the returned in-memory session is what xconn forwards
// bridged calls through (see App.Session).
func StartAppLayer(realm, socketPath string) (*xconn.Router, *xconn.Listener, *xconn.Session, error) {
	router, err := xconn.NewRouter(xconn.DefaultRouterConfig())
	if err != nil {
		return nil, nil, nil, err
	}
	if err := router.AddRealm(realm, &xconn.RealmConfig{
		AutoDiscloseCaller: true,
		Meta:               true,
		Roles: []xconn.RealmRole{{
			Name: "anonymous",
			Permissions: []xconn.Permission{{
				URI:            "",
				MatchPolicy:    wampproto.MatchPrefix,
				AllowCall:      true,
				AllowRegister:  true,
				AllowSubscribe: true,
			}},
		}},
	}); err != nil {
		router.Close()
		return nil, nil, nil, err
	}
	server := xconn.NewServer(router, nil, &xconn.ServerConfig{})
	listener, err := server.ListenAndServeRawSocket(xconn.NetworkUnix, socketPath)
	if err != nil {
		router.Close()
		return nil, nil, nil, err
	}

	session, err := xconn.ConnectInMemory(router, realm)
	if err != nil {
		_ = listener.Close()
		router.Close()
		return nil, nil, nil, err
	}
	return router, listener, session, nil
}

// NewDeviceRouter returns a router serving the device-facing realm that remote clients
// (cloud, LAN or standalone) call into.
func NewDeviceRouter(realm string) (*xconn.Router, error) {
	router, err := xconn.NewRouter(xconn.DefaultRouterConfig())
	if err != nil {
		return nil, err
	}

	permissions := []xconn.Permission{
		{
			URI:         xconnURIPrefix,
			MatchPolicy: wampproto.MatchPrefix,
			AllowCall:   true,
		},
		// WebRTC signaling, for clients that reach this router directly (standalone mode).
		{
			URI:            webrtcURIPrefix,
			MatchPolicy:    wampproto.MatchPrefix,
			AllowPublish:   true,
			AllowSubscribe: true,
		},
	}
	err = router.AddRealm(realm, &xconn.RealmConfig{
		AutoDiscloseCaller: true,
		Meta:               true,
		Roles: []xconn.RealmRole{
			{Name: "owner", Permissions: permissions},
			{Name: "admin", Permissions: permissions},
			{Name: "member", Permissions: permissions},
		},
	})
	if err != nil {
		router.Close()
		return nil, err
	}
	return router, nil
}
