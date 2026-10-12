package xconn

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/xconn-go"
	xconnwebrtc "github.com/xconnio/xconn-webrtc-go"
)

// CloudConfig is how Run attaches the device to the cloud and serves it on the LAN.
type CloudConfig struct {
	// Address is the cloud router's QUIC address (host:port), dialed with TLSConfig.
	Address   string
	TLSConfig *tls.Config
	// Realm is the cloud service's realm, joined alongside the device realm over the same
	// connection: authorized keys are listed and key and detach events published there.
	Realm string
	// ProcedureListKeys lists the principals authorized to connect to this device.
	ProcedureListKeys string
	// TopicKeyAddedFormat, TopicKeyRemovedFormat and TopicDetachFormat take the machine ID.
	TopicKeyAddedFormat   string
	TopicKeyRemovedFormat string
	TopicDetachFormat     string

	// CredentialsFile holds the device's Credentials, written once it is attached to the
	// cloud. Run waits for it to appear and removes it when the device is detached.
	CredentialsFile string
	// PrincipalsFile caches the authorized principals across restarts.
	PrincipalsFile string
	// LANPort is where the device realm is served over WebSocket to clients on the local
	// network (and advertised via mDNS).
	LANPort int
	// ICEServers for WebRTC; empty means DefaultICEServers.
	ICEServers []xconnwebrtc.ICEServer
}

// Run runs xconn for the cloud-attached device until ctx is done: it serves the device
// realm on the LAN and to the cloud, bridging app's procedures onto it, first waiting for
// the device to be attached and starting over after a detach.
func Run(ctx context.Context, app *App, cfg *CloudConfig) error {
	for {
		again, err := runDeviceSession(ctx, app, cfg)
		if err != nil || !again {
			return err
		}
	}
}

// runDeviceSession runs one connect/serve cycle: it bridges app onto the LAN-facing realm
// and runs the cloud reconnect loop, then blocks until either parent is done or a detach
// event. It returns true if the caller should start another cycle (detach happened), false
// to shut down.
func runDeviceSession(parent context.Context, app *App, cfg *CloudConfig) (bool, error) {
	cred, err := EnsureCredentials(parent, cfg.CredentialsFile)
	if err != nil {
		if parent.Err() != nil {
			return false, nil
		}
		return false, err
	}

	machineIDStr, err := machineID()
	if err != nil {
		return false, fmt.Errorf("failed to read machine-id: %w", err)
	}

	router, err := NewDeviceRouter(cred.Realm)
	if err != nil {
		return false, err
	}

	principals, err := ReadPrincipalsFromFile(cfg.PrincipalsFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		router.Close()
		return false, err
	}

	authenticator := NewAuthenticator(principals, cfg.PrincipalsFile)
	server := xconn.NewServer(router, authenticator, &xconn.ServerConfig{})
	listener, err := server.ListenAndServeWebSocket(xconn.NetworkTCP, fmt.Sprintf("0.0.0.0:%d", cfg.LANPort))
	if err != nil {
		router.Close()
		return false, err
	}
	defer listener.Close()

	localSession, err := xconn.ConnectInMemory(router, cred.Realm)
	if err != nil {
		router.Close()
		return false, err
	}

	// Bridge the app's procedures onto the LAN-facing realm -- any client reaching this
	// device directly (mDNS discovery + WebSocket, no cloud hop) gets the same procedures
	// as a cloud caller.
	if err := RegisterBridge(localSession, app); err != nil {
		router.Close()
		return false, err
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	detachChan := make(chan struct{}, 1)

	var cloudConnMu sync.Mutex
	var activeDeviceSess, activeCloudSess *xconn.QUICSession

	safeGo(func() {
		retryDelay := 1 * time.Second
		maxDelay := 30 * time.Second
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			cryptosignAuth, err := auth.NewCryptoSignAuthenticator(cred.AuthID, cred.PrivateKey, nil)
			if err != nil {
				log.Printf("failed to initialize cryptosign authenticator: %v", err)
				retryDelay = min(retryDelay*2, maxDelay)
				time.Sleep(retryDelay)
				continue
			}

			// Open the QUIC connection and the first WAMP session on the device realm.
			deviceSess, err := xconn.ConnectQUIC(ctx, cfg.Address, cred.Realm,
				&xconn.QUICDialerConfig{Authenticator: cryptosignAuth, TLSConfig: cfg.TLSConfig})
			if err != nil {
				if err.Error() == "wamp.error.no_such_realm" {
					select {
					case detachChan <- struct{}{}:
					default:
					}
				}
				log.Printf("failed to connect to cloud, will retry in %v: %v", retryDelay, err)
				retryDelay = min(retryDelay*2, maxDelay)
				time.Sleep(retryDelay)
				continue
			}

			// Open a second WAMP session on the cloud realm over the same QUIC connection.
			cloudSess, err := deviceSess.OpenSession(ctx, cfg.Realm,
				&xconn.QUICDialerConfig{Authenticator: cryptosignAuth})
			if err != nil {
				log.Printf("failed to open cloud realm session, will retry in %v: %v", retryDelay, err)
				_ = deviceSess.Close()
				retryDelay = min(retryDelay*2, maxDelay)
				time.Sleep(retryDelay)
				continue
			}

			deviceSession := deviceSess.Session
			cloudSession := cloudSess.Session

			cloudConnMu.Lock()
			activeDeviceSess = deviceSess
			activeCloudSess = cloudSess
			cloudConnMu.Unlock()

			log.Println("connected to cloud")

			// Relay raw streams opened by remote clients to the app.
			if app.StreamSocket != "" {
				safeGo(func() { acceptQUICStreams(deviceSess, app.StreamSocket) })
			}

			// Bridge the app's procedures onto the cloud-facing realm.
			if err := RegisterBridge(deviceSession, app); err != nil {
				log.Printf("failed to register procedures on cloud, will retry in %v: %v", retryDelay, err)
				_ = deviceSess.Connection().Close()
				retryDelay = min(retryDelay*2, maxDelay)
				time.Sleep(retryDelay)
				continue
			}

			// Fetch and maintain authorized principals via the cloud realm session.
			callResp := cloudSession.Call(cfg.ProcedureListKeys).Do()
			if callResp.Err != nil {
				log.Println("failed to list keys:", callResp.Err)
				_ = deviceSess.Connection().Close()
				retryDelay = min(retryDelay*2, maxDelay)
				time.Sleep(retryDelay)
				continue
			}

			if len(callResp.Args()) == 0 {
				log.Println("unexpected response from list keys: no args")
				_ = deviceSess.Connection().Close()
				retryDelay = min(retryDelay*2, maxDelay)
				time.Sleep(retryDelay)
				continue
			}

			jsonData, err := json.MarshalIndent(callResp.Args()[0], "", "  ")
			if err != nil {
				log.Println(err)
				_ = deviceSess.Connection().Close()
				retryDelay = min(retryDelay*2, maxDelay)
				time.Sleep(retryDelay)
				continue
			}

			var cryptosignPrincipals []*CryptosignPrincipal
			if err = json.Unmarshal(jsonData, &cryptosignPrincipals); err != nil {
				log.Println(err)
				_ = deviceSess.Connection().Close()
				retryDelay = min(retryDelay*2, maxDelay)
				time.Sleep(retryDelay)
				continue
			}

			jsonData = append(jsonData, '\n')
			if err = os.WriteFile(cfg.PrincipalsFile, jsonData, 0600); err != nil {
				log.Println(err)
			}

			authenticator.SetPrincipals(cryptosignPrincipals)
			keyAdded := fmt.Sprintf(cfg.TopicKeyAddedFormat, machineIDStr)
			keyRemoved := fmt.Sprintf(cfg.TopicKeyRemovedFormat, machineIDStr)
			if err := authenticator.SubscribeEvents(cloudSession, keyAdded, keyRemoved); err != nil {
				log.Println(err)
			}

			subResp := cloudSession.Subscribe(fmt.Sprintf(cfg.TopicDetachFormat, machineIDStr),
				func(event *xconn.Event) {
					select {
					case detachChan <- struct{}{}:
					default:
					}
				}).Do()
			if subResp.Err != nil {
				log.Println(subResp.Err)
			}

			if err := SetupWebRTC(deviceSession, router, authenticator, cfg.ICEServers, app.StreamSocket); err != nil {
				log.Printf("failed to setup webRtc provider, will retry in %v: %v", retryDelay, err)
				_ = deviceSess.Connection().Close()
				retryDelay = min(retryDelay*2, maxDelay)
				time.Sleep(retryDelay)
				continue
			}

			// Reset backoff after successful connection.
			retryDelay = 1 * time.Second

			// Both sessions share the QUIC connection; either ending means reconnect.
			select {
			case <-deviceSession.Done():
			case <-cloudSession.Done():
			}

			cloudConnMu.Lock()
			activeDeviceSess = nil
			activeCloudSess = nil
			cloudConnMu.Unlock()

			_ = deviceSess.Connection().Close()
			log.Println("disconnected from cloud, retrying...")
		}
	})

	select {
	case <-parent.Done():
		cancel()

		cloudConnMu.Lock()
		if activeCloudSess != nil {
			_ = activeCloudSess.Close()
		}
		if activeDeviceSess != nil {
			_ = activeDeviceSess.Close()
			_ = activeDeviceSess.Connection().Close()
		}
		cloudConnMu.Unlock()

		router.Close()
		return false, nil
	case <-detachChan:
		cancel()
		_ = os.Remove(cfg.CredentialsFile)

		cloudConnMu.Lock()
		if activeCloudSess != nil {
			_ = activeCloudSess.Close()
		}
		if activeDeviceSess != nil {
			_ = activeDeviceSess.Close()
			_ = activeDeviceSess.Connection().Close()
		}
		cloudConnMu.Unlock()

		router.Close()
		return true, nil
	}
}

// acceptQUICStreams runs an accept loop on sess, relaying each server-initiated stream to
// the app's streamSocket.
func acceptQUICStreams(sess *xconn.QUICSession, streamSocket string) {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		safeGo(func() { RelayStream(stream, streamSocket) })
	}
}
