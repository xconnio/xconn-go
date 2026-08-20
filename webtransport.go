package xconn

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/projectdiscovery/ratelimit"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/webtransport-go"
	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/transports"
)

// WebTransportDialerConfig holds configuration for a WebTransport client connection.
type WebTransportDialerConfig struct {
	SerializerSpec  SerializerSpec
	Authenticator   auth.ClientAuthenticator
	TLSClientConfig *tls.Config
	OutQueueSize    int
}

// GenerateWebTransportTLSConfig creates a short-lived self-signed TLS config for WebTransport.
// The certificate is valid for 14 days.
func GenerateWebTransportTLSConfig() (*tls.Config, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "xconn-webtransport"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(14*24*time.Hour - time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{certDER}, PrivateKey: key}},
	}
	hash := sha256.Sum256(certDER)
	return tlsCfg, hash[:], nil
}

// webTransportStreamConn adapts a *webtransport.Stream to net.Conn, capturing the session's
// addresses at construction time. Used on both the server and client sides.
type webTransportStreamConn struct {
	*webtransport.Stream
	localAddr  net.Addr
	remoteAddr net.Addr
}

func (c *webTransportStreamConn) LocalAddr() net.Addr  { return c.localAddr }
func (c *webTransportStreamConn) RemoteAddr() net.Addr { return c.remoteAddr }

// Read normalizes a clean shutdown (stream/session error code 0, as used by
// Close throughout this package) into io.EOF, matching the net.Conn
// convention that callers already handle for a closed stream.
func (c *webTransportStreamConn) Read(b []byte) (int, error) {
	n, err := c.Stream.Read(b)
	if err != nil {
		var streamErr *webtransport.StreamError
		if errors.As(err, &streamErr) && streamErr.ErrorCode == 0 {
			return n, io.EOF
		}

		var sessionErr *webtransport.SessionError
		if errors.As(err, &sessionErr) && sessionErr.ErrorCode == 0 {
			return n, io.EOF
		}
	}
	return n, err
}

func newWebTransportStreamConn(session *webtransport.Session, stream *webtransport.Stream) net.Conn {
	return &webTransportStreamConn{
		Stream:     stream,
		localAddr:  session.LocalAddr(),
		remoteAddr: session.RemoteAddr(),
	}
}

// WebTransportSession is a WAMP session over a WebTransport (HTTP/3) connection.
// It embeds Session for WAMP operations and exposes the underlying WebTransport connection
// for opening raw streams alongside the WAMP session.
type WebTransportSession struct {
	*Session
	conn *webtransport.Session
}

// Connection returns the underlying WebTransport session for advanced use.
func (w *WebTransportSession) Connection() *webtransport.Session {
	return w.conn
}

// OpenStream opens a raw (non-WAMP) bidirectional stream on the WebTransport connection.
func (w *WebTransportSession) OpenStream() (net.Conn, error) {
	stream, err := w.conn.OpenStreamSync(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to open WebTransport stream: %w", err)
	}
	return newWebTransportStreamConn(w.conn, stream), nil
}

// AcceptStream waits for the server to open a raw stream to this client.
func (w *WebTransportSession) AcceptStream() (net.Conn, error) {
	stream, err := w.conn.AcceptStream(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to accept WebTransport stream: %w", err)
	}
	return newWebTransportStreamConn(w.conn, stream), nil
}

// OpenSession opens an additional WAMP session on the same WebTransport connection.
// Each call opens a new stream and performs a fresh WAMP Hello/Welcome exchange,
// so multiple independent sessions can share one HTTP/3 connection.
func (w *WebTransportSession) OpenSession(ctx context.Context, realm string,
	config *WebTransportDialerConfig) (*WebTransportSession, error) {
	return openWebTransportSession(ctx, w.conn, realm, config)
}

// Close sends a WAMP GOODBYE and closes this session's stream.
// The underlying WebTransport connection is shared and remains open;
// call Connection().CloseWithError to shut it down entirely.
func (w *WebTransportSession) Close() error {
	err := w.Leave()
	_ = w.base.Close()
	return err
}

// openWebTransportSession opens one WAMP stream on an existing WebTransport session,
// performs the RawSocket handshake, and joins the given realm.
func openWebTransportSession(ctx context.Context, wtSess *webtransport.Session, realm string,
	config *WebTransportDialerConfig) (*WebTransportSession, error) {
	if config == nil {
		config = &WebTransportDialerConfig{}
	}
	if config.SerializerSpec == nil {
		config.SerializerSpec = CBORSerializerSpec
	}
	if config.Authenticator == nil {
		config.Authenticator = auth.NewAnonymousAuthenticator("", nil)
	}

	stream, err := wtSess.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open WebTransport stream: %w", err)
	}

	conn := newWebTransportStreamConn(wtSess, stream)
	serializerID := transports.Serializer(config.SerializerSpec.SerializerID())
	peer, err := rawSocketClientHandshake(conn, serializerID, config.OutQueueSize)
	if err != nil {
		return nil, err
	}

	base, err := Join(peer, realm, config.SerializerSpec.Serializer(), config.Authenticator)
	if err != nil {
		return nil, err
	}

	session := NewSession(base, config.SerializerSpec.Serializer()) //nolint:contextcheck
	return &WebTransportSession{Session: session, conn: wtSess}, nil
}

// WebTransportPeerSession is delivered on WebTransportListener.AcceptSession when a WAMP client
// authenticates over a WebTransport stream.
type WebTransportPeerSession struct {
	Ctx     context.Context
	Session BaseSession
}

// WebTransportStream is delivered on WebTransportListener.AcceptStream for raw (non-WAMP) streams.
type WebTransportStream struct {
	net.Conn
}

type WebTransportListener struct {
	*Listener
	server  *Server
	conns   chan *WebTransportPeerSession
	streams chan *WebTransportStream
	done    chan struct{}
	sync.Once
	sync.WaitGroup
}

// Close shuts down the WebTransport server and signals all internal goroutines to stop.
func (l *WebTransportListener) Close() error {
	err := l.Listener.Close()
	l.Do(func() { close(l.done) })
	return err
}

// AcceptSession returns a channel that receives an event each time a WAMP client authenticates.
func (l *WebTransportListener) AcceptSession() <-chan *WebTransportPeerSession {
	return l.conns
}

// AcceptStream returns a channel that receives an event each time a client opens a raw stream.
func (l *WebTransportListener) AcceptStream() <-chan *WebTransportStream {
	return l.streams
}

// ListenAndServeWebTransport starts an HTTP/3 server that upgrades WebTransport connections at
// the given URL path. Each WebTransport stream that begins with the RawSocket magic byte is treated
// as a WAMP session, other streams are delivered on AcceptStream.
func (s *Server) ListenAndServeWebTransport(address string, tlsConfig *tls.Config,
	path string) (*WebTransportListener, error) {
	if tlsConfig == nil {
		return nil, fmt.Errorf("tls config is required for WebTransport")
	}

	tlsConfig = tlsConfig.Clone()
	if !slices.Contains(tlsConfig.NextProtos, http3.NextProtoH3) {
		tlsConfig.NextProtos = append(tlsConfig.NextProtos, http3.NextProtoH3)
	}

	mux := http.NewServeMux()

	h3Server := &http3.Server{
		Addr:      address,
		TLSConfig: tlsConfig,
		Handler:   mux,
	}

	wtServer := &webtransport.Server{
		H3:          h3Server,
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	// Pre-bind the UDP socket so the actual address (including OS-assigned port
	// when ":0" is given) is known before returning the listener.
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve address: %w", err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to bind UDP socket: %w", err)
	}

	l := &WebTransportListener{
		Listener: &Listener{
			closer: wtServer,
			addr:   udpConn.LocalAddr(),
		},
		server:  s,
		conns:   make(chan *WebTransportPeerSession, 8),
		streams: make(chan *WebTransportStream, 8),
		done:    make(chan struct{}),
	}

	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		session, err := wtServer.Upgrade(w, r)
		if err != nil {
			log.Debugf("WebTransport upgrade failed from %s: %v", r.RemoteAddr, err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Debugf("WebTransport session established from %s", r.RemoteAddr)
		l.Add(1)
		go l.handleWebTransportSession(session) //nolint:contextcheck
	})

	go func() {
		if err := wtServer.Serve(udpConn); err != nil {
			log.Debugf("webtransport server stopped: %v", err)
			l.Do(func() { close(l.done) })
		}
	}()

	return l, nil
}

func (l *WebTransportListener) handleWebTransportSession(session *webtransport.Session) {
	defer l.Done()

	var streamWg sync.WaitGroup
	defer streamWg.Wait()

	for {
		stream, err := session.AcceptStream(context.Background())
		if err != nil {
			return
		}
		streamWg.Add(1)
		go func(st *webtransport.Stream) {
			defer streamWg.Done()
			l.dispatchWebTransportStream(session, st)
		}(stream)
	}
}

// dispatchWebTransportStream routes a stream: WAMP RawSocket magic (0x7F) → WAMP session,
// anything else → raw stream.
func (l *WebTransportListener) dispatchWebTransportStream(session *webtransport.Session, stream *webtransport.Stream) {
	streamConn := newWebTransportStreamConn(session, stream)

	br := bufio.NewReader(streamConn)
	magic, err := br.Peek(1)

	wrapped := connWithPrependedReader{
		Reader: br,
		Conn:   streamConn,
	}

	if err == nil && magic[0] == transports.MAGIC {
		l.handleWebTransportWAMPStream(wrapped)
	} else {
		select {
		case l.streams <- &WebTransportStream{Conn: wrapped}:
		case <-l.done:
		}
	}
}

// handleWebTransportWAMPStream runs a full WAMP session on a single WebTransport stream.
func (l *WebTransportListener) handleWebTransportWAMPStream(streamConn net.Conn) {
	s := l.server

	config := DefaultRawSocketServerConfig()
	config.KeepAliveInterval = s.keepAliveInterval
	config.KeepAliveTimeout = s.keepAliveTimeout
	config.OutQueueSize = s.outQueueSize

	base, err := s.rsAcceptor.Accept(streamConn, config)
	if err != nil {
		log.Debugf("failed to accept WebTransport WAMP stream: %v", err)
		return
	}

	if err = s.router.AttachClient(base); err != nil {
		log.Debugf("failed to attach WebTransport client: %v", err)
		return
	}

	sessCtx, sessCancel := context.WithCancel(context.Background())

	go func() {
		select {
		case l.conns <- &WebTransportPeerSession{Ctx: sessCtx, Session: base}:
		case <-l.done:
		}
	}()

	log.Debugf("attached webtransport client %d", base.ID())

	var limiter *ratelimit.Limiter
	if s.throttle != nil {
		limiter = s.throttle.Create()
	}

	for {
		msg, err := base.ReadMessage()
		if err != nil {
			log.Debugf("failed to read webtransport client message: %v", err)
			_ = s.router.DetachClient(base)
			break
		}

		if limiter != nil {
			limiter.Take()
		}

		if err = s.router.ReceiveMessage(base, msg); err != nil {
			log.Debugf("error feeding webtransport client message to router: %v", err)
		}
	}

	sessCancel()
	log.Debugf("detached webtransport client %d", base.ID())
}
