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

// QUICSession is the client-side WAMP session over a QUIC connection.
type QUICSession struct {
	*Session
	conn *quic.Conn
}

// Close sends a WAMP GOODBYE and closes the underlying QUIC connection.
func (q *QUICSession) Close() error {
	err := q.Leave()
	_ = q.conn.CloseWithError(0, "")
	return err
}

type QUICDialerConfig struct {
	SerializerSpec SerializerSpec
	Authenticator  auth.ClientAuthenticator
	TLSConfig      *tls.Config
	DialTimeout    time.Duration
	OutQueueSize   int
}

// DialQUIC connects to a WAMP router over QUIC.
// The first QUIC stream carries the WAMP protocol.
func DialQUIC(ctx context.Context, address, realm string, config *QUICDialerConfig) (*QUICSession, error) {
	if config == nil {
		config = &QUICDialerConfig{}
	}
	if config.SerializerSpec == nil {
		config.SerializerSpec = CBORSerializerSpec
	}
	if config.Authenticator == nil {
		config.Authenticator = auth.NewAnonymousAuthenticator("", nil)
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

	conn, err := quic.DialAddr(dialCtx, address, tlsConf, DefaultQuicConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", address, err)
	}

	wampStream, err := conn.OpenStreamSync(dialCtx)
	if err != nil {
		_ = conn.CloseWithError(0, "")
		return nil, fmt.Errorf("failed to open WAMP stream: %w", err)
	}

	serializerID := transports.Serializer(config.SerializerSpec.SerializerID())
	peer, err := rawSocketClientHandshake(newQUICStreamConn(conn, wampStream), serializerID, config.OutQueueSize)
	if err != nil {
		_ = conn.CloseWithError(0, "")
		return nil, err
	}

	base, err := Join(peer, realm, config.SerializerSpec.Serializer(), config.Authenticator)
	if err != nil {
		_ = conn.CloseWithError(0, "")
		return nil, err
	}

	session := NewSession(base, config.SerializerSpec.Serializer()) //nolint:contextcheck
	return &QUICSession{Session: session, conn: conn}, nil
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
