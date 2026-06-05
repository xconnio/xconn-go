package xconn

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/xconnio/wampproto-go/auth"
	"github.com/xconnio/wampproto-go/transports"
)

// YamuxSession is the client-side WAMP session over a yamux-multiplexed connection.
type YamuxSession struct {
	*Session
	yamuxSess *yamux.Session
}

// Close sends a WAMP GOODBYE and closes the underlying yamux session.
func (y *YamuxSession) Close() error {
	err := y.Leave()
	_ = y.yamuxSess.Close()
	return err
}

type YamuxDialerConfig struct {
	SerializerSpec    SerializerSpec
	Authenticator     auth.ClientAuthenticator
	NetDial           func(ctx context.Context, network, addr string) (net.Conn, error)
	DialTimeout       time.Duration
	KeepAliveInterval time.Duration
	KeepAliveTimeout  time.Duration
	OutQueueSize      int
}

// DialYamux connects to a WAMP router over a yamux-multiplexed connection.
// The first yamux stream carries the WAMP protocol.
func DialYamux(ctx context.Context, address, realm string, config *YamuxDialerConfig) (*YamuxSession, error) {
	if config == nil {
		config = &YamuxDialerConfig{}
	}
	if config.SerializerSpec == nil {
		config.SerializerSpec = CBORSerializerSpec
	}
	if config.Authenticator == nil {
		config.Authenticator = auth.NewAnonymousAuthenticator("", nil)
	}

	dialCtx := ctx
	if config.DialTimeout != 0 {
		var cancel context.CancelFunc
		dialCtx, cancel = context.WithTimeout(ctx, config.DialTimeout)
		defer cancel()
	}

	var conn net.Conn
	var err error
	if config.NetDial != nil {
		conn, err = config.NetDial(dialCtx, "tcp", address)
	} else {
		var d net.Dialer
		conn, err = d.DialContext(dialCtx, "tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", address, err)
	}

	yamuxSess, err := yamux.Client(conn, nil)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to create yamux session: %w", err)
	}

	wampStream, err := yamuxSess.Open()
	if err != nil {
		_ = yamuxSess.Close()
		return nil, fmt.Errorf("failed to open WAMP stream: %w", err)
	}

	serializerID := transports.Serializer(config.SerializerSpec.SerializerID())
	peer, err := rawSocketClientHandshake(wampStream, serializerID, config.OutQueueSize)
	if err != nil {
		_ = yamuxSess.Close()
		return nil, err
	}

	base, err := Join(peer, realm, config.SerializerSpec.Serializer(), config.Authenticator)
	if err != nil {
		_ = yamuxSess.Close()
		return nil, err
	}

	session := NewSession(base, config.SerializerSpec.Serializer()) //nolint:contextcheck
	return &YamuxSession{Session: session, yamuxSess: yamuxSess}, nil
}

// rawSocketClientHandshake performs the client-side RawSocket handshake on an existing conn.
// This is used to run WAMP RawSocket framing over a yamux stream.
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
	if _, err = conn.Read(responseHeader); err != nil {
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
