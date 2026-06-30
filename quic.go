package xconn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"slices"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/transports"
)

// NextProtoWAMP is the ALPN protocol negotiated for WAMP-over-QUIC connections.
const NextProtoWAMP = "wamp.2.quic"

func DefaultQuicConfig() *quic.Config {
	return &quic.Config{
		MaxIncomingStreams:    1024,
		MaxIncomingUniStreams: 1024,
	}
}

// quicStreamConn adapts a *quic.Stream to net.Conn.
type quicStreamConn struct {
	*quic.Stream

	localAddr  net.Addr
	remoteAddr net.Addr
}

func (c *quicStreamConn) LocalAddr() net.Addr {
	return c.localAddr
}

func (c *quicStreamConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func newQUICStreamConn(conn *quic.Conn, stream *quic.Stream) net.Conn {
	return &quicStreamConn{
		Stream:     stream,
		localAddr:  conn.LocalAddr(),
		remoteAddr: conn.RemoteAddr(),
	}
}

// QUICConn is delivered on QUICListener.Conns when a QUIC client connects and authenticates.
// Ctx is canceled when the client disconnects, allowing callers to clean up.
type QUICConn struct {
	Ctx     context.Context
	Session BaseSession
	Conn    *QUICClientConn
}

// QUICStream is delivered on QUICListener.AcceptStream when a client opens a raw stream.
type QUICStream struct {
	net.Conn
}

// QUICClientConn is the server-side handle for a connected QUIC client.
// It allows the server to open raw streams to the client.
type QUICClientConn struct {
	conn *quic.Conn
}

// OpenStream opens a new raw stream to the connected QUIC client.
func (c *QUICClientConn) OpenStream() (net.Conn, error) {
	stream, err := c.conn.OpenStreamSync(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to open quic stream: %w", err)
	}
	return newQUICStreamConn(c.conn, stream), nil
}

// QUICConnection owns a single QUIC connection and lets callers open multiple
// independent WAMP sessions (and raw streams) over it.
type QUICConnection struct {
	conn *quic.Conn
}

// OpenSession opens an additional WAMP session on this QUIC connection.
// Each call opens a new stream and performs a full WAMP handshake.
func (c *QUICConnection) OpenSession(ctx context.Context, realm string, cfg *QUICDialerConfig) (*QUICSession, error) {
	return openQUICSession(ctx, c, realm, cfg)
}

// OpenRawStream opens a raw (non-WAMP) stream for application-level data transfer.
func (c *QUICConnection) OpenRawStream(ctx context.Context) (net.Conn, error) {
	stream, err := c.conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open quic stream: %w", err)
	}
	return newQUICStreamConn(c.conn, stream), nil
}

// AcceptRawStream waits for the server to open a raw stream on this connection.
func (c *QUICConnection) AcceptRawStream(ctx context.Context) (net.Conn, error) {
	stream, err := c.conn.AcceptStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to accept quic stream: %w", err)
	}
	return newQUICStreamConn(c.conn, stream), nil
}

// Close closes the QUIC connection, terminating all sessions and streams on it immediately.
func (c *QUICConnection) Close() error {
	return c.conn.CloseWithError(0, "")
}

// QUICSession is one WAMP session over a single stream of a QUICConnection.
// Multiple QUICSessions can coexist on the same QUICConnection.
type QUICSession struct {
	*Session
	conn *QUICConnection
}

// Connection returns the underlying QUICConnection shared across all sessions.
func (q *QUICSession) Connection() *QUICConnection {
	return q.conn
}

// OpenSession opens an additional WAMP session on the same QUIC connection.
func (q *QUICSession) OpenSession(ctx context.Context, realm string, config *QUICDialerConfig) (*QUICSession, error) {
	return q.conn.OpenSession(ctx, realm, config)
}

// OpenStream opens a raw (non-WAMP) stream for data transfer.
func (q *QUICSession) OpenStream() (net.Conn, error) {
	return q.conn.OpenRawStream(context.Background())
}

// AcceptStream waits for the server to open a raw stream to this client.
func (q *QUICSession) AcceptStream() (net.Conn, error) {
	return q.conn.AcceptRawStream(context.Background())
}

// Close sends WAMP GOODBYE and closes this session's WAMP stream.
func (q *QUICSession) Close() error {
	err := q.Leave()
	_ = q.base.Close()
	return err
}

type QUICDialerConfig struct {
	SerializerSpec SerializerSpec
	Authenticator  auth.ClientAuthenticator
	TLSConfig      *tls.Config
	DialTimeout    time.Duration
	OutQueueSize   int
}

// DialQUIC connects to a WAMP router over QUIC and opens the first WAMP session.
// Additional sessions can be opened on the same connection via QUICSession.OpenSession.
func DialQUIC(ctx context.Context, address, realm string, config *QUICDialerConfig) (*QUICSession, error) {
	if config == nil {
		config = &QUICDialerConfig{}
	}

	tlsConf := config.TLSConfig
	if tlsConf == nil {
		tlsConf = &tls.Config{NextProtos: []string{NextProtoWAMP}}
	} else if !slices.Contains(tlsConf.NextProtos, NextProtoWAMP) {
		tlsConf = tlsConf.Clone()
		tlsConf.NextProtos = append(tlsConf.NextProtos, NextProtoWAMP)
	}

	dialCtx := ctx
	if config.DialTimeout != 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, config.DialTimeout)
		defer cancel()
	}

	rawConn, err := quic.DialAddr(dialCtx, address, tlsConf, DefaultQuicConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", address, err)
	}

	quicConn := &QUICConnection{conn: rawConn}
	sess, err := openQUICSession(ctx, quicConn, realm, config)
	if err != nil {
		_ = rawConn.CloseWithError(0, "")
		return nil, err
	}
	return sess, nil
}

// openQUICSession opens one WAMP stream on quicConn, performs the RawSocket
// handshake, and joins the given realm.
func openQUICSession(ctx context.Context, quicConn *QUICConnection, realm string,
	config *QUICDialerConfig) (*QUICSession, error) {
	if config == nil {
		config = &QUICDialerConfig{}
	}
	if config.SerializerSpec == nil {
		config.SerializerSpec = CBORSerializerSpec
	}
	if config.Authenticator == nil {
		config.Authenticator = auth.NewAnonymousAuthenticator("", nil)
	}

	wampStream, err := quicConn.conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAMP stream: %w", err)
	}

	serializerID := transports.Serializer(config.SerializerSpec.SerializerID())
	peer, err := rawSocketClientHandshake(newQUICStreamConn(quicConn.conn, wampStream), serializerID, config.OutQueueSize)
	if err != nil {
		return nil, err
	}

	base, err := Join(peer, realm, config.SerializerSpec.Serializer(), config.Authenticator)
	if err != nil {
		return nil, err
	}

	session := NewSession(base, config.SerializerSpec.Serializer()) //nolint:contextcheck
	return &QUICSession{Session: session, conn: quicConn}, nil
}

// GenerateSelfSignedTLSConfig creates a self-signed TLS config for use with ListenAndServeQUIC.
func GenerateSelfSignedTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "xconn-quic"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	cert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{NextProtoWAMP},
	}, nil
}

// rawSocketClientHandshake performs the client-side RawSocket handshake on an existing conn.
// This is used to run WAMP RawSocket framing over a QUIC stream.
func rawSocketClientHandshake(conn net.Conn, serializer transports.Serializer, outQueueSize int) (Peer, error) {
	header := transports.NewHandshake(serializer, transports.DefaultMaxMsgSize)
	headerRaw, err := transports.SendHandshake(header)
	if err != nil {
		return nil, fmt.Errorf("failed to build handshake: %w", err)
	}

	if _, err = conn.Write(headerRaw); err != nil {
		return nil, fmt.Errorf("failed to send handshake: %w", err)
	}

	responseHeader := make([]byte, 4)
	if _, err = io.ReadFull(conn, responseHeader); err != nil {
		return nil, fmt.Errorf("failed to read handshake response: %w", err)
	}

	if _, err = transports.ReceiveHandshake(responseHeader); err != nil {
		return nil, fmt.Errorf("failed to parse handshake response: %w", err)
	}

	if outQueueSize == 0 {
		outQueueSize = ClientOutQueueSizeDefault
	}

	return NewRawSocketPeer(conn, RawSocketPeerConfig{
		Serializer:   serializer,
		OutQueueSize: outQueueSize,
	}), nil
}
