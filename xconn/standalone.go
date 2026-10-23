package xconn

import (
	"context"
	"crypto/tls"
	"errors"
	"net"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/xconn-go"
	xconnwebrtc "github.com/xconnio/xconn-webrtc-go"
)

// WebTransportPath is the URL path the WebTransport listener serves WAMP on.
const WebTransportPath = "/wamp"

// StandaloneConfig is how RunStandalone serves the device without a cloud account.
type StandaloneConfig struct {
	// QUICAddress, if set, is where to serve the device over QUIC (host:port, UDP).
	QUICAddress string
	// WebTransportAddress, if set, is where to serve the device over WebTransport
	// (host:port, UDP), at WebTransportPath.
	WebTransportAddress string
	// WebSocketAddress, if set, is where to serve the device over WebSocket (host:port, TCP),
	// for WAMP clients with neither QUIC nor WebTransport. It's plain, without TLS, so best
	// kept to localhost or a network you trust. It carries WAMP only: no raw streams.
	WebSocketAddress string
	// TLSConfig holds the server certificate for QUIC and WebTransport.
	TLSConfig *tls.Config
	// Realm is the device realm served.
	Realm string
	// Keys are the cryptosign public keys (hex) allowed to connect.
	Keys []string
	// Passwords are the wampcra username -> password pairs allowed to connect.
	Passwords map[string]string
	// Authenticator, if set, decides who may connect instead of Keys and Passwords, so
	// who may can change while the device is served. It must give its sessions one of the
	// device realm's authroles (see NewDeviceRouter): owner, as Keys and Passwords do, admin
	// or member.
	Authenticator auth.ServerAuthenticator
	// ICEServers for WebRTC; empty means DefaultICEServers.
	ICEServers []xconnwebrtc.ICEServer
}

// RunStandalone serves the device realm over QUIC, WebTransport and/or WebSocket until ctx is done,
// bridging app's procedures onto it and relaying raw streams and WebRTC data channels to
// the app.
func RunStandalone(ctx context.Context, app *App, cfg *StandaloneConfig) error {
	if cfg.QUICAddress == "" && cfg.WebTransportAddress == "" && cfg.WebSocketAddress == "" {
		return errors.New("standalone mode needs a QUIC, WebTransport or WebSocket address")
	}
	if cfg.TLSConfig == nil && (cfg.QUICAddress != "" || cfg.WebTransportAddress != "") {
		return errors.New("standalone mode needs a TLS config")
	}

	router, err := NewDeviceRouter(cfg.Realm)
	if err != nil {
		return err
	}
	defer router.Close()

	authenticator := cfg.Authenticator
	if authenticator == nil {
		authenticator = NewStandaloneAuthenticator(cfg.Keys, cfg.Passwords)
	}
	server := xconn.NewServer(router, authenticator, &xconn.ServerConfig{})

	// A nil channel never delivers, so a disabled transport simply drops out of the select.
	var quicStreams <-chan *xconn.QUICStream
	var wtStreams <-chan *xconn.WebTransportStream
	if cfg.QUICAddress != "" {
		listener, err := server.ListenAndServeQUIC(cfg.QUICAddress, cfg.TLSConfig)
		if err != nil {
			return err
		}
		defer listener.Close()
		drain(listener.AcceptSession())
		quicStreams = listener.AcceptStream()
		log.Printf("standalone mode: serving realm %s over QUIC on %s", cfg.Realm, listener.Addr())
	}
	if cfg.WebTransportAddress != "" {
		listener, err := server.ListenAndServeWebTransport(cfg.WebTransportAddress, cfg.TLSConfig, WebTransportPath)
		if err != nil {
			return err
		}
		defer listener.Close()
		drain(listener.AcceptSession())
		wtStreams = listener.AcceptStream()
		log.Printf("standalone mode: serving realm %s over WebTransport on %s (path %s)", cfg.Realm,
			listener.Addr(), WebTransportPath)
	}
	if cfg.WebSocketAddress != "" {
		listener, err := server.ListenAndServeWebSocket(xconn.NetworkTCP, cfg.WebSocketAddress)
		if err != nil {
			return err
		}
		defer listener.Close()
		log.Printf("standalone mode: serving realm %s over WebSocket on %s", cfg.Realm, listener.Addr())
	}

	localSession, err := xconn.ConnectInMemory(router, cfg.Realm)
	if err != nil {
		return err
	}
	if err := RegisterBridge(localSession, app); err != nil {
		return err
	}
	if err := SetupWebRTC(localSession, router, authenticator, cfg.ICEServers, app.StreamSocket); err != nil {
		return err
	}
	if cfg.Authenticator == nil {
		log.Printf("standalone mode: %d key(s) and %d user(s) authorized", len(cfg.Keys), len(cfg.Passwords))
	}

	relay := func(stream net.Conn) {
		if app.StreamSocket == "" {
			_ = stream.Close()
			return
		}
		safeGo(func() { RelayStream(stream, app.StreamSocket) })
	}
	for {
		select {
		case stream, ok := <-quicStreams:
			if !ok {
				return errors.New("QUIC listener stopped")
			}
			relay(stream.Conn)
		case stream, ok := <-wtStreams:
			if !ok {
				return errors.New("WebTransport listener stopped")
			}
			relay(stream.Conn)
		case <-ctx.Done():
			return nil
		}
	}
}

// drain discards a listener's session events: sessions are already attached to the router,
// and the listener blocks a goroutine per session until its event is received.
func drain[T any](events <-chan T) {
	safeGo(func() {
		for range events { //nolint:revive // only draining
		}
	})
}
