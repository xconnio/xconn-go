package xconn

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
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
		KeepAlivePeriod:       15 * time.Second,
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

// Read normalizes a clean shutdown (application error code 0, as used by
// Close throughout this package) into io.EOF, matching the net.Conn
// convention that callers already handle for a closed stream.
func (c *quicStreamConn) Read(b []byte) (int, error) {
	n, err := c.Stream.Read(b)
	if err != nil {
		var appErr *quic.ApplicationError
		if errors.As(err, &appErr) && appErr.ErrorCode == 0 {
			return n, io.EOF
		}
	}
	return n, err
}

func newQUICStreamConn(conn *quic.Conn, stream *quic.Stream) net.Conn {
	return &quicStreamConn{
		Stream:     stream,
		localAddr:  conn.LocalAddr(),
		remoteAddr: conn.RemoteAddr(),
	}
}

// QUICPeerSession is delivered on QUICListener.Conns when a QUIC client connects and authenticates.
// Ctx is canceled when the client disconnects, allowing callers to clean up.
type QUICPeerSession struct {
	Ctx     context.Context
	Session BaseSession
	*QUICConn
}

// QUICStream is delivered on QUICListener.AcceptStream when a client opens a raw stream.
type QUICStream struct {
	net.Conn
}

// QUICConn is the underlying QUIC connection, used for opening and accepting raw streams.
type QUICConn struct {
	conn *quic.Conn
}

// OpenRawStream opens a raw (non-WAMP) stream for application-level data transfer.
func (c *QUICConn) OpenRawStream(ctx context.Context) (net.Conn, error) {
	stream, err := c.conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open quic stream: %w", err)
	}
	return newQUICStreamConn(c.conn, stream), nil
}

// OpenStream opens a raw stream using a background context.
func (c *QUICConn) OpenStream() (net.Conn, error) {
	return c.OpenRawStream(context.Background())
}

// AcceptRawStream waits for the remote side to open a raw stream on this connection.
func (c *QUICConn) AcceptRawStream(ctx context.Context) (net.Conn, error) {
	stream, err := c.conn.AcceptStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to accept quic stream: %w", err)
	}
	return newQUICStreamConn(c.conn, stream), nil
}

// Close closes the QUIC connection, terminating all sessions and streams on it immediately.
func (c *QUICConn) Close() error {
	return c.conn.CloseWithError(0, "")
}

// QUICSession is one WAMP session over a single stream of a QUICConn.
// Multiple QUICSessions can coexist on the same QUICConn.
type QUICSession struct {
	*Session
	conn *QUICConn
}

// Connection returns the underlying QUICConn shared across all sessions.
func (q *QUICSession) Connection() *QUICConn {
	return q.conn
}

// OpenSession opens an additional WAMP session on the same QUIC connection.
func (q *QUICSession) OpenSession(ctx context.Context, realm string, config *QUICDialerConfig) (*QUICSession, error) {
	return openQUICSession(ctx, q.conn, realm, config)
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
	SerializerSpec  SerializerSpec
	Authenticator   auth.ClientAuthenticator
	TLSConfig       *tls.Config
	DialTimeout     time.Duration
	OutQueueSize    int
	KeepAlivePeriod time.Duration
}

// openQUICSession opens one WAMP stream on quicConn, performs the RawSocket
// handshake, and joins the given realm.
func openQUICSession(ctx context.Context, quicConn *QUICConn, realm string,
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
