package xconn

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	log "github.com/sirupsen/logrus"

	"github.com/xconnio/wampproto-go/messages"
	"github.com/xconnio/wampproto-go/serializers"
	"github.com/xconnio/wampproto-go/transports"
)

const (
	ClientOutQueueSizeDefault = 16
	RouterOutQueueSizeDefault = 64
)

func NewBaseSession(id uint64, realm, authID, authRole, authMethod string, authExtra map[string]any, cl Peer,
	serializer serializers.Serializer) BaseSession {
	return &baseSession{
		id:         id,
		realm:      realm,
		authID:     authID,
		authRole:   authRole,
		authMethod: authMethod,
		authExtra:  authExtra,
		client:     cl,
		serializer: serializer,
		tracker: &messageTracker{
			stopTrackCh: make(chan struct{}),
		},
	}
}

type messageTracker struct {
	enabled     bool
	rxCount     uint64
	txCount     uint64
	rxPerSec    uint64
	txPerSec    uint64
	stopTrackCh chan struct{}
}

type baseSession struct {
	id         uint64
	realm      string
	authID     string
	authRole   string
	authMethod string
	authExtra  map[string]any

	client     Peer
	serializer serializers.Serializer

	session     *Session
	publishLogs bool
	topic       string

	tracker *messageTracker

	sync.Mutex
}

func (b *baseSession) AuthMethod() string {
	return b.authMethod
}

func (b *baseSession) AuthExtra() map[string]any {
	return b.authExtra
}

func (b *baseSession) EnableLogPublishing(session *Session, topic string) {
	b.setMessageRateTracking(true)
	b.Lock()
	defer b.Unlock()

	b.session = session
	b.topic = topic
	b.publishLogs = true
}

func (b *baseSession) DisableLogPublishing() {
	b.setMessageRateTracking(false)
	b.Lock()
	defer b.Unlock()

	b.session = nil
	b.topic = ""
	b.publishLogs = false
}

func (b *baseSession) setMessageRateTracking(enabled bool) {
	if enabled {
		b.Lock()
		if b.tracker.enabled {
			b.Unlock()
			return // already running
		}
		b.tracker.enabled = true
		b.Unlock()
		ticker := time.NewTicker(time.Second)
		go func() {
			for {
				select {
				case <-ticker.C:
					b.Lock()
					rx := b.tracker.rxCount
					tx := b.tracker.txCount
					b.tracker.rxCount = 0
					b.tracker.txCount = 0
					b.tracker.rxPerSec = rx
					b.tracker.txPerSec = tx
					b.Unlock()

				case <-b.tracker.stopTrackCh:
					ticker.Stop()
					return
				}
			}
		}()
	} else {
		select {
		case b.tracker.stopTrackCh <- struct{}{}:
		default:
		}
		b.Lock()
		b.tracker.enabled = false
		b.Unlock()
	}
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

	var pub *PublishRequest
	b.Lock()
	if b.tracker.enabled {
		b.tracker.rxCount++
	}

	if b.publishLogs {
		pub = b.session.Publish(b.topic).Arg(map[string]any{
			"message":                constructReceivedMsgLog(msg),
			"rx_messages_per_second": b.tracker.rxPerSec,
			"tx_messages_per_second": b.tracker.txPerSec,
		})
	}
	b.Unlock()

	if pub != nil {
		_ = pub.Do()
	}

	return msg, nil
}

func (b *baseSession) constructWriteMessage(message messages.Message) ([]byte, error) {
	payload, err := b.serializer.Serialize(message)
	if err != nil {
		return nil, err
	}

	var pub *PublishRequest
	b.Lock()
	if b.tracker.enabled {
		b.tracker.txCount++
	}

	if b.publishLogs {
		pub = b.session.Publish(b.topic).Arg(map[string]any{
			"message":                constructSendingMsgLog(message),
			"rx_messages_per_second": b.tracker.rxPerSec,
			"tx_messages_per_second": b.tracker.txPerSec,
		})
	}
	b.Unlock()

	if pub != nil {
		_ = pub.Do()
	}

	return payload, nil
}

func (b *baseSession) WriteMessage(message messages.Message) error {
	payload, err := b.constructWriteMessage(message)
	if err != nil {
		return err
	}

	return b.Write(payload)
}

func (b *baseSession) TryWriteMessage(message messages.Message) (bool, error) {
	payload, err := b.constructWriteMessage(message)
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
	return b.client.Close()
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
	doneWriting   chan struct{}

	pingCh    chan struct{}
	wsMsgOp   ws.OpCode
	server    bool
	closed    bool
	closeChan chan struct{}

	sync.Mutex
}

func NewWebSocketPeer(conn net.Conn, peerConfig WSPeerConfig) (Peer, error) {
	msgOpCode := ws.OpBinary
	if !peerConfig.Binary {
		msgOpCode = ws.OpText
	}

	if peerConfig.OutQueueSize == 0 {
		peerConfig.OutQueueSize = ClientOutQueueSizeDefault
	}

	peer := &WebSocketPeer{
		transportType: TransportWebSocket,
		protocol:      peerConfig.Protocol,
		conn:          conn,
		pingCh:        make(chan struct{}, 1),
		wsMsgOp:       msgOpCode,
		server:        peerConfig.Server,
		writeChan:     make(chan []byte, peerConfig.OutQueueSize),
		ctrlChan:      make(chan wsMsg, 2),
		doneWriting:   make(chan struct{}),
		closeChan:     make(chan struct{}),
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
	closed := c.closed
	c.Unlock()

	if closed {
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
	defer close(c.doneWriting)

	for {
		select {
		case data := <-c.writeChan:
			if err := c.writeOpFunc(c.conn, c.wsMsgOp, data); err != nil {
				log.Debugf("failed to write websocket message: %v", err)
				_ = c.Close()
				return
			}
		case ctrlMsg := <-c.ctrlChan:
			if err := c.writeOpFunc(c.conn, ctrlMsg.opCode, ctrlMsg.payload); err != nil {
				_ = c.Close()
				return
			}
		case <-c.closeChan:
			return
		}
	}
}

func (c *WebSocketPeer) startPinger(keepaliveInterval time.Duration, keepaliveTimeout time.Duration) {
	if keepaliveInterval == 0 {
		keepaliveInterval = 30 * time.Second
	}

	if keepaliveTimeout == 0 {
		keepaliveTimeout = 10 * time.Second
	}

	if keepaliveTimeout >= keepaliveInterval {
		log.Printf("adjusting keepaliveTimeout (%s) to half of interval (%s)", keepaliveTimeout, keepaliveInterval)
		keepaliveTimeout = keepaliveInterval / 2
	}

	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
		case <-c.closeChan:
			return
		}

		// Send a ping
		randomBytes := make([]byte, 4)
		_, err := rand.Read(randomBytes)
		if err != nil {
			log.Println("failed to generate random bytes:", err)
			continue
		}

		select {
		case c.ctrlChan <- wsMsg{payload: randomBytes, opCode: ws.OpPing}:
		case <-c.closeChan:
			return
		default:
			log.Println("failed to send ping: ctrlChan full")
			_ = c.Close()
			return
		}

		select {
		case <-c.pingCh:
		case <-time.After(keepaliveTimeout):
			log.Println("ping timeout, closing connection")
			_ = c.Close()
			return
		case <-c.closeChan:
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
	for {
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
			case <-c.closeChan:
				return nil, io.ErrClosedPipe
			default:
				log.Println("failed to send pong: ctrlChan full")
				continue
			}
		case ws.OpPong:
			select {
			case c.pingCh <- struct{}{}:
			default:
			}

			continue
		case ws.OpClose:
			_ = c.Close()
			return nil, fmt.Errorf("connection closed")
		}
	}
}

func (c *WebSocketPeer) Write(bytes []byte) error {
	c.Lock()
	closed := c.closed
	c.Unlock()

	if closed {
		return io.ErrClosedPipe
	}

	if len(c.writeChan) == cap(c.writeChan) {
		start := time.Now()
		c.writeChan <- bytes
		log.Debugf("websocket write channel unblocked. took=%s", time.Since(start))
		return nil
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
	if c.closed {
		c.Unlock()
		return nil
	}
	c.closed = true
	c.Unlock()

	close(c.closeChan)

	select {
	case <-c.doneWriting:
	case <-time.After(1000 * time.Millisecond):
		// writer still busy, timeout
	}

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
	doneWriting   chan struct{}
	closed        bool

	sync.Mutex
}

func NewRawSocketPeer(conn net.Conn, peerConfig RawSocketPeerConfig) Peer {
	if peerConfig.OutQueueSize == 0 {
		peerConfig.OutQueueSize = ClientOutQueueSizeDefault
	}

	peer := &RawSocketPeer{
		transportType: TransportRawSocket,
		conn:          conn,
		serializer:    peerConfig.Serializer,
		writeChan:     make(chan []byte, peerConfig.OutQueueSize),
		ctrlChan:      make(chan rsMsg, 2),
		doneWriting:   make(chan struct{}),
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

func writeFull(w io.Writer, buf []byte) error {
	totalWritten := 0
	for totalWritten < len(buf) {
		n, err := w.Write(buf[totalWritten:])
		if err != nil {
			return err
		}

		if n == 0 {
			return io.ErrShortWrite
		}

		totalWritten += n
	}

	return nil
}

func (r *RawSocketPeer) writer() {
	defer close(r.doneWriting)

	for {
		select {
		case payload := <-r.writeChan:
			header := transports.NewMessageHeader(transports.MessageWamp, len(payload))
			if err := writeFull(r.conn, transports.SendMessageHeader(header)); err != nil {
				_ = r.Close()
				return
			}

			if err := writeFull(r.conn, payload); err != nil {
				_ = r.Close()
				return
			}
		case ctrlMsg := <-r.ctrlChan:
			header := transports.NewMessageHeader(ctrlMsg.msgType, len(ctrlMsg.payload))
			if err := writeFull(r.conn, transports.SendMessageHeader(header)); err != nil {
				_ = r.Close()
				return
			}

			if err := writeFull(r.conn, ctrlMsg.payload); err != nil {
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
	_, err := io.ReadFull(r.conn, headerRaw)
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

	var now time.Time
	if len(r.writeChan) == cap(r.writeChan) {
		now = time.Now()
		log.Debugf("rawsocket write channel blocked (full)")
	}

	r.writeChan <- data

	if !now.IsZero() {
		log.Debugf("rawsocket write channel unblocked. took=%s", time.Since(now))
	}

	return nil
}

func (r *RawSocketPeer) Close() error {
	r.Lock()
	defer r.Unlock()

	if r.closed {
		return nil
	}

	select {
	case <-r.doneWriting:
	case <-time.After(1000 * time.Millisecond):
		// writer still busy, timeout
	}

	r.closed = true
	return r.conn.Close()
}

type localPeer struct {
	conn  net.Conn
	other net.Conn

	writeChan   chan []byte
	doneWriting chan struct{}
	closed      bool

	rmu sync.Mutex
	wmu sync.Mutex
}

func (l *localPeer) Type() TransportType {
	return TransportInMemory
}

func (l *localPeer) NetConn() net.Conn {
	return l.conn
}

func (l *localPeer) read() ([]byte, error) {
	l.rmu.Lock()
	defer l.rmu.Unlock()

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

func (l *localPeer) writer() {
	defer close(l.doneWriting)

	for data := range l.writeChan {
		length := uint32(len(data)) // #nosec
		if err := binary.Write(l.conn, binary.BigEndian, length); err != nil {
			_ = l.other.Close()
			return
		}

		if err := writeFull(l.conn, data); err != nil {
			_ = l.other.Close()
			return
		}
	}
}

func (l *localPeer) Write(data []byte) error {
	l.wmu.Lock()
	defer l.wmu.Unlock()

	if l.closed {
		return io.ErrClosedPipe
	}

	var now time.Time
	if len(l.writeChan) == cap(l.writeChan) {
		now = time.Now()
		log.Debugln("localpeer write channel blocked (full)")
	}

	l.writeChan <- data

	if !now.IsZero() {
		log.Debugf("localpeer write channel unblocked. took=%s", time.Since(now))
	}

	return nil
}

func (l *localPeer) TryWrite(data []byte) (bool, error) {
	l.wmu.Lock()
	defer l.wmu.Unlock()

	if l.closed {
		return false, io.ErrClosedPipe
	}

	select {
	case l.writeChan <- data:
		return true, nil
	default:
		return false, nil
	}
}

func (l *localPeer) Close() error {
	l.wmu.Lock()
	defer l.wmu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true

	close(l.writeChan)

	select {
	case <-l.doneWriting:
	case <-time.After(1 * time.Second):
		// writer still busy, timeout
	}

	return l.conn.Close()
}

func newLocalPeer(conn, otherSide net.Conn, outQueueSize int) *localPeer {
	if outQueueSize == 0 {
		outQueueSize = ClientOutQueueSizeDefault
	}

	p := &localPeer{
		conn:        conn,
		other:       otherSide,
		writeChan:   make(chan []byte, outQueueSize),
		doneWriting: make(chan struct{}),
	}

	go p.writer()
	return p
}

func NewInMemoryPeerPair(outQueueSize int) (Peer, Peer) {
	conn1, conn2 := net.Pipe()
	peer1 := newLocalPeer(conn1, conn2, outQueueSize)
	peer2 := newLocalPeer(conn2, conn1, outQueueSize)

	return peer1, peer2
}
