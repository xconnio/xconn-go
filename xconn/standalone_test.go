package xconn_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/xconn-go"
	xconnd "github.com/xconnio/xconn-go/xconn"
)

const (
	testAppRealm    = "io.xconn.test.app"
	testDeviceRealm = "io.xconn.test.device"
	testProcedure   = "io.xconn.test.echo"
	testUser        = "carol"
	testPassword    = "s3cret"

	transportQUIC         = "quic"
	transportWebTransport = "webtransport"
)

// device is a standalone device served by startStandalone.
type device struct {
	quicAddr, wtURL string
	streams         net.Listener
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
	dir := t.TempDir()

	appRouter, appListener, appSession, err := xconnd.StartAppLayer(testAppRealm, filepath.Join(dir, "app.sock"))
	require.NoError(t, err)
	t.Cleanup(appRouter.Close)
	t.Cleanup(func() { _ = appListener.Close() })

	callee, err := xconn.ConnectInMemory(appRouter, testAppRealm)
	require.NoError(t, err)
	resp := callee.Register(testProcedure, func(_ context.Context, inv *xconn.Invocation) *xconn.InvocationResult {
		return xconn.NewInvocationResult(inv.Args()...)
	}).Do()
	require.NoError(t, resp.Err)

	streamListener, err := net.Listen("unix", filepath.Join(dir, "streams.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = streamListener.Close() })

	tlsConfig, err := xconn.GenerateSelfSignedTLSConfig()
	require.NoError(t, err)

	d := &device{quicAddr: freeUDPAddr(t), streams: streamListener}
	wtAddr := freeUDPAddr(t)
	d.wtURL = "https://" + wtAddr + xconnd.WebTransportPath

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- xconnd.RunStandalone(ctx, &xconnd.App{
			Session:      appSession,
			Procedures:   []string{testProcedure},
			StreamSocket: streamListener.Addr().String(),
		}, &xconnd.StandaloneConfig{
			QUICAddress:         d.quicAddr,
			WebTransportAddress: wtAddr,
			TLSConfig:           tlsConfig,
			Realm:               testDeviceRealm,
			Keys:                keys,
			Passwords:           passwords,
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
		})
	}
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
