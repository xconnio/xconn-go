package xconn

import (
	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/serializers"
	"github.com/xconnio/xconn-go"
	xconnwebrtc "github.com/xconnio/xconn-webrtc-go"
)

// DefaultICEServers is the STUN-only ICE configuration.
func DefaultICEServers() []xconnwebrtc.ICEServer {
	return []xconnwebrtc.ICEServer{{URLs: []string{DefaultSTUNServer}}}
}

// SetupWebRTC answers WebRTC offers made on session (signaling for P2P), attaching the
// resulting WAMP-over-WebRTC sessions to router and, when streamSocket is set, relaying
// their other data channels to it (see RelayHeader).
func SetupWebRTC(session *xconn.Session, router *xconn.Router, authenticator auth.ServerAuthenticator,
	iceServers []xconnwebrtc.ICEServer, streamSocket string) error {
	if len(iceServers) == 0 {
		iceServers = DefaultICEServers()
	}
	webRtcManager := xconnwebrtc.NewWebRTCHandler()
	if err := webRtcManager.Setup(&xconnwebrtc.ProviderConfig{
		Session:                     session,
		ProcedureHandleOffer:        ProcedureWebRTCOffer,
		TopicHandleRemoteCandidates: TopicAnswererOnCandidate,
		TopicPublishLocalCandidate:  TopicOffererOnCandidate,
		Serializer:                  &serializers.CBORSerializer{},
		Authenticator:               authenticator,
		Router:                      router,
		ICEServers:                  iceServers,
	}); err != nil {
		return err
	}
	if streamSocket != "" {
		webRtcManager.OnDataChannel(handleDataChannel(streamSocket))
	}
	return nil
}
