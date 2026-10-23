package xconn_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/serializers"
	"github.com/xconnio/xconn-go"
	xconnd "github.com/xconnio/xconn-go/xconn"
)

const (
	testAppRealm    = "io.xconn.test.app"
	testDeviceRealm = "io.xconn.test.device"
	testProcedure   = "io.xconn.test.echo"
	testPrefix      = "io.xconn.test.app."
	testCaller      = "io.xconn.test.caller"
	testUser        = "carol"
	testPassword    = "s3cret"

	transportQUIC         = "quic"
	transportWebTransport = "webtransport"
)

// device is a standalone device served by startStandalone.
type device struct {
	quicAddr, wtURL string
	streams         net.Listener
	// left gets the app-layer sessions that left.
	left chan uint64
}

// session is a client connection to a device: a WAMP session plus its raw streams.
type session struct {
	*xconn.Session
	openStream func() (net.Conn, error)
}

// freeUDPAddr returns a loopback UDP address that is free right now.
func freeUDPAddr(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := conn.LocalAddr().String()
	require.NoError(t, conn.Close())
	return addr
}

// startStandalone runs an app layer with an echo procedure and serves it in standalone mode
// over QUIC and WebTransport, with a generated self-signed certificate.
func startStandalone(t *testing.T, keys []string, passwords map[string]string) *device {
	t.Helper()
	return startStandaloneWith(t, &xconnd.StandaloneConfig{Keys: keys, Passwords: passwords})
}

// startStandaloneWith is startStandalone with cfg's Keys, Passwords and Authenticator.
func startStandaloneWith(t *testing.T, cfg *xconnd.StandaloneConfig) *device {
	t.Helper()
	dir := t.TempDir()

	appRouter, appListener, appSession, err := xconnd.StartAppLayer(testAppRealm, filepath.Join(dir, "app.sock"))
	require.NoError(t, err)
	t.Cleanup(appRouter.Close)
	t.Cleanup(func() { _ = appListener.Close() })

	callee, err := xconn.ConnectInMemory(appRouter, testAppRealm)
	require.NoError(t, err)
	echo := func(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
		return xconn.NewInvocationResult(inv.Args()...)
	}
	for _, procedure := range []string{testProcedure, testPrefix + "echo"} {
		resp := callee.Register(procedure, echo).Do()
		require.NoError(t, resp.Err)
	}
	require.NoError(t, callee.Register(testCaller, func(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
		return xconn.NewInvocationResult(inv.Caller(), inv.CallerAuthID(), inv.CallerAuthRole())
	}).Do().Err)
	left := make(chan uint64, 16)
	require.NoError(t, callee.Subscribe("wamp.session.on_leave", func(event *xconn.Event) {
		if id, err := event.ArgUInt64(0); err == nil {
			left <- id
		}
	}).Do().Err)

	streamListener, err := net.Listen("unix", filepath.Join(dir, "streams.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = streamListener.Close() })

	tlsConfig, err := xconn.GenerateSelfSignedTLSConfig()
	require.NoError(t, err)

	d := &device{quicAddr: freeUDPAddr(t), streams: streamListener, left: left}
	wtAddr := freeUDPAddr(t)
	d.wtURL = "https://" + wtAddr + xconnd.WebTransportPath

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- xconnd.RunStandalone(ctx, &xconnd.App{
			Session: appSession,
			NewSession: func(authID, authRole string) (*xconn.Session, error) {
				base, err := xconn.ConnectInMemoryBase(appRouter, testAppRealm, authID, authRole,
					&serializers.MsgPackSerializer{}, 16)
				if err != nil {
					return nil, err
				}
				return xconn.NewSession(base, base.Serializer()), nil
			},
			Procedures:   []string{testProcedure, testCaller},
			Prefixes:     []string{testPrefix},
			StreamSocket: streamListener.Addr().String(),
		}, &xconnd.StandaloneConfig{
			QUICAddress:         d.quicAddr,
			WebTransportAddress: wtAddr,
			TLSConfig:           tlsConfig,
			Realm:               testDeviceRealm,
			Keys:                cfg.Keys,
			Passwords:           cfg.Passwords,
			Authenticator:       cfg.Authenticator,
		})
	}()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})
	return d
}

// clientTLSConfig trusts the test's throwaway self-signed certificate.
func clientTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test certificate
}

// connect joins the device realm over transport ("quic" or "webtransport"), retrying while
// the listeners come up.
func connect(t *testing.T, d *device, transport string, authenticator auth.ClientAuthenticator) (*session, error) {
	t.Helper()
	tlsConfig := clientTLSConfig()

	var s *session
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		switch transport {
		case transportQUIC:
			var qs *xconn.QUICSession
			qs, err = xconn.ConnectQUIC(ctx, d.quicAddr, testDeviceRealm,
				&xconn.QUICDialerConfig{Authenticator: authenticator, TLSConfig: tlsConfig})
			if err == nil {
				s = &session{Session: qs.Session, openStream: qs.OpenStream}
			}
		case transportWebTransport:
			var ws *xconn.WebTransportSession
			ws, err = xconn.ConnectWebTransport(ctx, d.wtURL, testDeviceRealm,
				&xconn.WebTransportDialerConfig{Authenticator: authenticator, TLSClientConfig: tlsConfig})
			if err == nil {
				s = &session{Session: ws.Session, openStream: ws.OpenStream}
			}
		default:
			panic(fmt.Sprintf("unknown transport %q", transport))
		}
		cancel()
		if err == nil {
			t.Cleanup(func() { _ = s.Leave() })
			return s, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func cra(user, password string) auth.ClientAuthenticator {
	return auth.NewWAMPCRAAuthenticator(user, password, nil)
}

var transports = []string{transportQUIC, transportWebTransport} //nolint:gochecknoglobals

func TestRunStandaloneBridgesProcedures(t *testing.T) {
	d := startStandalone(t, nil, map[string]string{testUser: testPassword})
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			s, err := connect(t, d, transport, cra(testUser, testPassword))
			require.NoError(t, err)
			require.Equal(t, testUser, s.Details().AuthID())

			resp := s.Call(testProcedure).Args("hello").Do()
			require.NoError(t, resp.Err)
			require.Equal(t, "hello", resp.Args()[0])

			resp = s.Call(testPrefix + "echo").Args("prefixed").Do()
			require.NoError(t, resp.Err)
			require.Equal(t, "prefixed", resp.Args()[0])

			resp = s.Call(testPrefix + "missing").Do()
			require.ErrorContains(t, resp.Err, "no_such_procedure")
		})
	}
}

func TestRunStandaloneSessionPerCaller(t *testing.T) {
	d := startStandalone(t, nil, map[string]string{testUser: testPassword})
	appCaller := func(s *session) uint64 {
		resp := s.Call(testCaller).Do()
		require.NoError(t, resp.Err)
		id, err := resp.ArgUInt64(0)
		require.NoError(t, err)
		// The app sees the remote caller, not the bridge.
		require.Equal(t, testUser, resp.ArgStringOr(1, ""))
		require.Equal(t, "owner", resp.ArgStringOr(2, ""))
		return id
	}

	a, err := connect(t, d, transportQUIC, cra(testUser, testPassword))
	require.NoError(t, err)
	b, err := connect(t, d, transportQUIC, cra(testUser, testPassword))
	require.NoError(t, err)

	callerA := appCaller(a)
	require.Equal(t, callerA, appCaller(a))
	require.NotEqual(t, callerA, appCaller(b))

	require.NoError(t, a.Leave())
	require.Eventually(t, func() bool {
		select {
		case id := <-d.left:
			return id == callerA
		default:
			return false
		}
	}, 5*time.Second, 10*time.Millisecond)
}

func TestRunStandaloneRelaysStreams(t *testing.T) {
	d := startStandalone(t, nil, map[string]string{testUser: testPassword})
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			s, err := connect(t, d, transport, cra(testUser, testPassword))
			require.NoError(t, err)

			stream, err := s.openStream()
			require.NoError(t, err)
			defer stream.Close()
			_, err = stream.Write([]byte("ping"))
			require.NoError(t, err)

			relayed, err := d.streams.Accept()
			require.NoError(t, err)
			defer relayed.Close()

			header, err := xconnd.ReadRelayHeader(relayed)
			require.NoError(t, err)
			require.Equal(t, xconnd.RelayKindStream, header.Kind)

			buf := make([]byte, 4)
			_, err = io.ReadFull(relayed, buf)
			require.NoError(t, err)
			require.Equal(t, "ping", string(buf))

			_, err = relayed.Write([]byte("pong"))
			require.NoError(t, err)
			_, err = io.ReadFull(stream, buf)
			require.NoError(t, err)
			require.Equal(t, "pong", string(buf))
		})
	}
}

func TestStandaloneAuthenticator(t *testing.T) {
	pub, priv, err := auth.GenerateCryptoSignKeyPair()
	require.NoError(t, err)
	_, otherPriv, err := auth.GenerateCryptoSignKeyPair()
	require.NoError(t, err)

	d := startStandalone(t, []string{pub}, map[string]string{testUser: testPassword})
	cryptosign := func(privateKey string) auth.ClientAuthenticator {
		a, err := auth.NewCryptoSignAuthenticator("anyone", privateKey, nil)
		require.NoError(t, err)
		return a
	}
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			_, err := connect(t, d, transport, cryptosign(priv))
			require.NoError(t, err)
			_, err = connect(t, d, transport, cra(testUser, testPassword))
			require.NoError(t, err)

			for _, a := range []auth.ClientAuthenticator{cryptosign(otherPriv), cra(testUser, "wrong"),
				cra("bob", testPassword)} {
				err := connectOnce(d, transport, a)
				require.Error(t, err)
			}
		})
	}
}

// changingAuthenticator lets in the wampcra users it's told about at the time, as owners.
type changingAuthenticator struct {
	mu        sync.Mutex
	passwords map[string]string
}

func (a *changingAuthenticator) Methods() []auth.Method { return []auth.Method{auth.WAMPCRA} }

func (a *changingAuthenticator) Authenticate(request auth.Request) (auth.Response, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	password, ok := a.passwords[request.AuthID()]
	if !ok {
		return nil, fmt.Errorf("unknown authid %s", request.AuthID())
	}
	return auth.NewCRAResponse(request.AuthID(), "owner", password, 0), nil
}

func TestStandaloneWithAuthenticator(t *testing.T) {
	authenticator := &changingAuthenticator{passwords: map[string]string{}}
	d := startStandaloneWith(t, &xconnd.StandaloneConfig{
		Keys: []string{"ignored with an authenticator"}, Authenticator: authenticator,
	})
	for _, transport := range transports {
		t.Run(transport, func(t *testing.T) {
			require.Error(t, connectOnce(d, transport, cra(testUser, testPassword)), "not yet")

			authenticator.mu.Lock()
			authenticator.passwords[testUser] = testPassword
			authenticator.mu.Unlock()
			s, err := connect(t, d, transport, cra(testUser, testPassword))
			require.NoError(t, err, "let in once it's told, while the device is served")
			resp := s.Call(testCaller).Do()
			require.NoError(t, resp.Err)
			require.Equal(t, testUser, resp.ArgStringOr(1, ""))
			require.Equal(t, "owner", resp.ArgStringOr(2, ""))
			require.NoError(t, s.Leave())

			authenticator.mu.Lock()
			delete(authenticator.passwords, testUser)
			authenticator.mu.Unlock()
			require.Error(t, connectOnce(d, transport, cra(testUser, testPassword)), "and not once it's told otherwise")
		})
	}
}

// connectOnce is connect without retries, for connections expected to fail.
func connectOnce(d *device, transport string, authenticator auth.ClientAuthenticator) error {
	tlsConfig := clientTLSConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if transport == transportQUIC {
		s, err := xconn.ConnectQUIC(ctx, d.quicAddr, testDeviceRealm,
			&xconn.QUICDialerConfig{Authenticator: authenticator, TLSConfig: tlsConfig})
		if err == nil {
			_ = s.Close()
		}
		return err
	}
	s, err := xconn.ConnectWebTransport(ctx, d.wtURL, testDeviceRealm,
		&xconn.WebTransportDialerConfig{Authenticator: authenticator, TLSClientConfig: tlsConfig})
	if err == nil {
		_ = s.Close()
	}
	return err
}
