package xconn

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
)

func NewBaseSession(id int64, realm, authID, authRole string, cl Peer, serializer serializers.Serializer) BaseSession {
	return &baseSession{
		id:         id,
		realm:      realm,
		authID:     authID,
		authRole:   authRole,
		client:     cl,
		serializer: serializer,
	}
}

type baseSession struct {
	id       int64
	realm    string
	authID   string
	authRole string

	client     Peer
	serializer serializers.Serializer
}

func (b *baseSession) Serializer() serializers.Serializer {
	return b.serializer
}

func (b *baseSession) ReadMessage() (messages.Message, error) {
	payload, err := b.Read()
	if err != nil {
		return nil, err
	}

	msg, err := b.serializer.Deserialize(payload)
	if err != nil {
		return nil, err
	}

	return msg, nil
}

func (b *baseSession) WriteMessage(message messages.Message) error {
	payload, err := b.serializer.Serialize(message)
	if err != nil {
		return err
	}

	return b.Write(payload)
}

func (b *baseSession) ID() int64 {
	return b.id
}

func (b *baseSession) Realm() string {
	return b.realm
}

func (b *baseSession) AuthID() string {
	return b.authID
}

func (b *baseSession) AuthRole() string {
	return b.authRole
}

func (b *baseSession) NetConn() net.Conn {
	return b.client.NetConn()
}

func (b *baseSession) Read() ([]byte, error) {
	return b.client.Read()
}

func (b *baseSession) Write(payload []byte) error {
	return b.client.Write(payload)
}

func (b *baseSession) Close() error {
	return b.client.NetConn().Close()
}

func NewWebSocketPeer(conn net.Conn, peerConfig WSPeerConfig) (Peer, error) {
	peer := &WebSocketPeer{
		transportType: TransportWebSocket,
		protocol:      peerConfig.Protocol,
		conn:          conn,
		pingCh:        make(chan struct{}, 1),
		binary:        peerConfig.Binary,
		server:        peerConfig.Server,
	}

	if peerConfig.KeepAliveInterval != 0 {
		// Start ping-pong handling
		go peer.startPinger(peerConfig.KeepAliveInterval, peerConfig.KeepAliveTimeout)
	}

	return peer, nil
}

type WebSocketPeer struct {
	transportType TransportType
	protocol      string
	conn          net.Conn

	pingCh chan struct{}
	binary bool
	server bool

	sync.Mutex
}

func (c *WebSocketPeer) startPinger(keepaliveInterval time.Duration, keepaliveTimeout time.Duration) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	if keepaliveTimeout == 0 {
		keepaliveTimeout = 10 * time.Second
	}
	for {
		<-ticker.C
		// Send a ping
		randomBytes := make([]byte, 4)
		_, err := rand.Read(randomBytes)
		if err != nil {
			fmt.Println("failed to generate random bytes:", err)
		}
		if err := c.writeOpFunc(c.conn, ws.OpPing, randomBytes); err != nil {
			log.Printf("failed to send ping: %v\n", err)
			_ = c.conn.Close()
			return
		}

		select {
		case <-c.pingCh:
		case <-time.After(keepaliveTimeout):
			log.Println("ping timeout, closing connection")
			_ = c.conn.Close()
			return
		}
	}
}

func (c *WebSocketPeer) peerState() ws.State {
	if c.server {
		return ws.StateServerSide
	}
	return ws.StateClientSide
}

func (c *WebSocketPeer) writeOpFunc(w io.Writer, op ws.OpCode, p []byte) error {
	if c.server {
		return wsutil.WriteServerMessage(w, op, p)
	}

	return wsutil.WriteClientMessage(w, op, p)
}

func (c *WebSocketPeer) Read() ([]byte, error) {
	header, reader, err := wsutil.NextReader(c.conn, c.peerState())
	if err != nil {
		return nil, err
	}

	payload := make([]byte, header.Length)
	_, err = reader.Read(payload)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	switch header.OpCode {
	case ws.OpText, ws.OpBinary:
		return payload, nil
	case ws.OpPing:
		if err = c.writeOpFunc(c.conn, ws.OpPong, payload); err != nil {
			return nil, fmt.Errorf("failed to send pong: %w", err)
		}
	case ws.OpPong:
		c.pingCh <- struct{}{}
	case ws.OpClose:
		_ = c.conn.Close()
		return nil, fmt.Errorf("connection closed")
	}

	return c.Read()
}

func (c *WebSocketPeer) Write(bytes []byte) error {
	c.Lock()
	defer c.Unlock()
	if c.binary {
		return c.writeOpFunc(c.conn, ws.OpBinary, bytes)
	}

	return c.writeOpFunc(c.conn, ws.OpText, bytes)
}

func (c *WebSocketPeer) Type() TransportType {
	return c.transportType
}

func (c *WebSocketPeer) Protocol() string {
	return c.protocol
}

func (c *WebSocketPeer) NetConn() net.Conn {
	return c.conn
}
