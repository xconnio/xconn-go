package xconn

import (
	"crypto/rand"
	"encoding/binary"
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
	"github.com/xconnio/wampproto-go/transports"
)

func NewBaseSession(id uint64, realm, authID, authRole string, cl Peer, serializer serializers.Serializer) BaseSession {
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
	id       uint64
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

func (b *baseSession) TryWriteMessage(message messages.Message) (bool, error) {
	payload, err := b.serializer.Serialize(message)
	if err != nil {
		return false, err
	}

	return b.TryWrite(payload)
}

func (b *baseSession) ID() uint64 {
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

func (b *baseSession) TryWrite(bytes []byte) (bool, error) {
	return b.client.TryWrite(bytes)
}

func (b *baseSession) Close() error {
	return b.client.NetConn().Close()
}

type wsMsg struct {
	opCode  ws.OpCode
	payload []byte
}

type WebSocketPeer struct {
	transportType TransportType
	protocol      string
	conn          net.Conn
	writeChan     chan []byte
	ctrlChan      chan wsMsg

	pingCh  chan struct{}
	wsMsgOp ws.OpCode
	server  bool
	closed  bool

	sync.Mutex
}

func NewWebSocketPeer(conn net.Conn, peerConfig WSPeerConfig) (Peer, error) {
	msgOpCode := ws.OpBinary
	if !peerConfig.Binary {
		msgOpCode = ws.OpText
	}

	peer := &WebSocketPeer{
		transportType: TransportWebSocket,
		protocol:      peerConfig.Protocol,
		conn:          conn,
		pingCh:        make(chan struct{}, 1),
		wsMsgOp:       msgOpCode,
		server:        peerConfig.Server,
		writeChan:     make(chan []byte, 64),
		ctrlChan:      make(chan wsMsg, 2),
	}

	if peerConfig.KeepAliveInterval != 0 {
		// Start ping-pong handling
		go peer.startPinger(peerConfig.KeepAliveInterval, peerConfig.KeepAliveTimeout)
	}

	go peer.writer()
	return peer, nil
}

func (c *WebSocketPeer) TryWrite(data []byte) (bool, error) {
	c.Lock()
	defer c.Unlock()

	if c.closed {
		return false, io.ErrClosedPipe
	}

	select {
	case c.writeChan <- data:
		return true, nil // successfully queued
	default:
		return false, nil // channel full => would block
	}
}

func (c *WebSocketPeer) writer() {
	for {
		select {
		case data := <-c.writeChan:
			if err := c.writeOpFunc(c.conn, c.wsMsgOp, data); err != nil {
				_ = c.Close()
				return
			}
		case ctrlMsg := <-c.ctrlChan:
			if err := c.writeOpFunc(c.conn, ctrlMsg.opCode, ctrlMsg.payload); err != nil {
				_ = c.Close()
				return
			}
		}
	}
}

func (c *WebSocketPeer) startPinger(keepaliveInterval time.Duration, keepaliveTimeout time.Duration) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	if keepaliveTimeout == 0 {
		keepaliveTimeout = 30 * time.Second
	}

	for {
		<-ticker.C
		// Send a ping
		randomBytes := make([]byte, 4)
		_, err := rand.Read(randomBytes)
		if err != nil {
			fmt.Println("failed to generate random bytes:", err)
		}

		select {
		case c.ctrlChan <- wsMsg{payload: randomBytes, opCode: ws.OpPing}:
		default:
			log.Printf("failed to send ping: %v\n", err)
			_ = c.Close()
			return
		}

		select {
		case <-c.pingCh:
		case <-time.After(keepaliveTimeout):
			log.Println("ping timeout, closing connection")
			_ = c.Close()
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

	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	switch header.OpCode {
	case ws.OpText, ws.OpBinary:
		return payload, nil
	case ws.OpPing:
		select {
		case c.ctrlChan <- wsMsg{payload: payload, opCode: ws.OpPong}:
		default:
			return nil, errors.New("failed to send pong: channel is full")
		}
	case ws.OpPong:
		select {
		case c.pingCh <- struct{}{}:
		default:
		}
	case ws.OpClose:
		_ = c.Close()
		return nil, fmt.Errorf("connection closed")
	}

	return c.Read()
}

func (c *WebSocketPeer) Write(bytes []byte) error {
	c.Lock()
	defer c.Unlock()

	if c.closed {
		return io.ErrClosedPipe
	}

	c.writeChan <- bytes
	return nil
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

func (c *WebSocketPeer) Close() error {
	c.Lock()
	defer c.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	return c.conn.Close()
}

const maxRawSocketPayload = 16 * 1024 * 1024 // 16 MB

type rsMsg struct {
	msgType transports.Message
	payload []byte
}

type RawSocketPeer struct {
	transportType TransportType
	conn          net.Conn
	serializer    transports.Serializer
	writeChan     chan []byte
	ctrlChan      chan rsMsg
	closed        bool

	sync.Mutex
}

func NewRawSocketPeer(conn net.Conn, peerConfig RawSocketPeerConfig) Peer {
	peer := &RawSocketPeer{
		transportType: TransportRawSocket,
		conn:          conn,
		serializer:    peerConfig.Serializer,
		writeChan:     make(chan []byte, 64),
		ctrlChan:      make(chan rsMsg, 2),
	}

	go peer.writer()
	return peer
}

func (r *RawSocketPeer) TryWrite(data []byte) (bool, error) {
	if len(data) > maxRawSocketPayload {
		return false, fmt.Errorf("rawsocket payload too large: %d bytes (max %d)", len(data), maxRawSocketPayload)
	}

	r.Lock()
	defer r.Unlock()

	if r.closed {
		return false, io.ErrClosedPipe
	}

	select {
	case r.writeChan <- data:
		return true, nil // successfully queued
	default:
		return false, nil // channel full => would block
	}
}

func (r *RawSocketPeer) writer() {
	for {
		select {
		case payload := <-r.writeChan:
			header := transports.NewMessageHeader(transports.MessageWamp, len(payload))
			if _, err := r.conn.Write(transports.SendMessageHeader(header)); err != nil {
				_ = r.Close()
				return
			}

			if _, err := r.conn.Write(payload); err != nil {
				_ = r.Close()
				return
			}
		case ctrlMsg := <-r.ctrlChan:
			header := transports.NewMessageHeader(ctrlMsg.msgType, len(ctrlMsg.payload))
			if _, err := r.conn.Write(transports.SendMessageHeader(header)); err != nil {
				_ = r.Close()
				return
			}

			if _, err := r.conn.Write(ctrlMsg.payload); err != nil {
				_ = r.Close()
				return
			}
		}
	}
}

func (r *RawSocketPeer) Type() TransportType {
	return r.transportType
}

func (r *RawSocketPeer) NetConn() net.Conn {
	return r.conn
}

func (r *RawSocketPeer) Read() ([]byte, error) {
	headerRaw := make([]byte, 4)
	_, err := r.conn.Read(headerRaw)
	if err != nil {
		return nil, err
	}

	header, err := transports.ReceiveMessageHeader(headerRaw)
	if err != nil {
		return nil, err
	}

	if header.Length() > maxRawSocketPayload {
		return nil, fmt.Errorf("rawsocket payload too large: %d bytes (max %d)", header.Length(), maxRawSocketPayload)
	}

	payload := make([]byte, header.Length())
	_, err = io.ReadFull(r.conn, payload)
	if err != nil {
		return nil, err
	}

	if header.Kind() == transports.MessageWamp {
		return payload, nil
	} else if header.Kind() == transports.MessagePing {
		r.ctrlChan <- rsMsg{payload: payload, msgType: transports.MessagePong}

		return r.Read()
	} else if header.Kind() == transports.MessagePong {
		// FIXME: implement a timer that gets reset on successful arrival of pong
		return r.Read()
	} else {
		return nil, fmt.Errorf("unknown message type: %v", header.Kind())
	}
}

func (r *RawSocketPeer) Write(data []byte) error {
	if len(data) > maxRawSocketPayload {
		return fmt.Errorf("rawsocket payload too large: %d bytes (max %d)", len(data), maxRawSocketPayload)
	}

	r.Lock()
	defer r.Unlock()

	if r.closed {
		return io.ErrClosedPipe
	}

	r.writeChan <- data
	return nil
}

func (r *RawSocketPeer) Close() error {
	r.Lock()
	defer r.Unlock()

	if r.closed {
		return nil
	}

	r.closed = true
	return r.conn.Close()
}

type localPeer struct {
	conn  net.Conn
	other net.Conn
}

func (l *localPeer) Type() TransportType {
	return TransportInMemory
}

func (l *localPeer) NetConn() net.Conn {
	return l.conn
}

func (l *localPeer) read() ([]byte, error) {
	var length uint32

	if err := binary.Read(l.conn, binary.BigEndian, &length); err != nil {
		return nil, err
	}

	msg := make([]byte, length)
	_, err := io.ReadFull(l.conn, msg)
	if err != nil {
		return nil, err
	}

	return msg, nil
}

func (l *localPeer) Read() ([]byte, error) {
	data, err := l.read()
	if err != nil {
		_ = l.other.Close()
		return nil, err
	}

	return data, nil
}

func (l *localPeer) write(data []byte) error {
	length := uint32(len(data)) // #nosec

	if err := binary.Write(l.conn, binary.BigEndian, length); err != nil {
		return err
	}

	written := 0
	for written < len(data) {
		n, err := l.conn.Write(data[written:])
		if err != nil {
			return err
		}

		if n == 0 {
			return io.ErrShortWrite
		}

		written += n
	}

	return nil
}

func (l *localPeer) Write(data []byte) error {
	if err := l.write(data); err != nil {
		_ = l.other.Close()
		return err
	}

	return nil
}

func (l *localPeer) TryWrite(bytes []byte) (bool, error) {
	if err := l.Write(bytes); err != nil {
		return false, err
	}

	return true, nil
}

func (l *localPeer) Close() error {
	return l.conn.Close()
}

func newLocalPeer(conn, otherSide net.Conn) *localPeer {
	return &localPeer{
		conn:  conn,
		other: otherSide,
	}
}

func NewInMemoryPeerPair() (Peer, Peer) {
	conn1, conn2 := net.Pipe()
	peer1 := newLocalPeer(conn1, conn2)
	peer2 := newLocalPeer(conn2, conn1)

	return peer1, peer2
}
