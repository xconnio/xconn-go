package xconn

import (
	"net"
	"sync"

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

func NewWebSocketPeer(conn net.Conn, protocol string, binary, server bool) (Peer, error) {
	var wsReader ReaderFunc
	var wsWriter WriterFunc
	var err error
	if server {
		wsReader, wsWriter, err = ServerSideWSReaderWriter(binary)
	} else {
		wsReader, wsWriter, err = ClientSideWSReaderWriter(binary)
	}

	if err != nil {
		return nil, err
	}

	return &WebSocketPeer{
		transportType: TransportWebSocket,
		protocol:      protocol,
		conn:          conn,
		wsReader:      wsReader,
		wsWriter:      wsWriter,
	}, nil
}

type WebSocketPeer struct {
	transportType TransportType
	protocol      string
	conn          net.Conn
	wsReader      ReaderFunc
	wsWriter      WriterFunc

	wm sync.Mutex
}

func (c *WebSocketPeer) Read() ([]byte, error) {
	return c.wsReader(c.conn)
}

func (c *WebSocketPeer) Write(bytes []byte) error {
	c.wm.Lock()
	defer c.wm.Unlock()

	return c.wsWriter(c.conn, bytes)
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
