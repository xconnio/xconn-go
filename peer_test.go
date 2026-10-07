package xconn_test

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xconnio/wampproto-go/transports"
	"github.com/xconnio/xconn-go"
)

func TestInMemoryPeer(t *testing.T) {
	client, server := xconn.NewInMemoryPeerPair(0)

	clientChan := make(chan []byte)
	clientCloseChan := make(chan struct{})
	go func() {
		data, err := client.Read()
		require.NoError(t, err)
		clientChan <- data

		_, err = client.Read()
		require.Error(t, err)

		clientCloseChan <- struct{}{}
	}()

	serverChan := make(chan []byte)
	serverCloseChan := make(chan struct{})
	go func() {
		data, err := server.Read()
		require.NoError(t, err)
		serverChan <- data

		_, err = client.Read()
		require.Error(t, err)

		serverCloseChan <- struct{}{}
	}()

	data := make([]byte, 1024)
	go func() {
		err := server.Write(data)
		require.NoError(t, err)
	}()

	go func() {
		err := client.Write(data)
		require.NoError(t, err)
	}()

	require.Eventually(t, func() bool {
		clientData := <-clientChan
		require.Equal(t, data, clientData)
		return true
	}, time.Second, 50*time.Millisecond)

	require.Eventually(t, func() bool {
		serverData := <-serverChan
		require.Equal(t, data, serverData)
		return true
	}, time.Second, 50*time.Millisecond)

	err := client.NetConn().Close()
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		<-clientCloseChan
		return true
	}, time.Second, 50*time.Millisecond)

	require.Eventually(t, func() bool {
		<-serverCloseChan
		return true
	}, time.Second, 50*time.Millisecond)
}

// blockedWriteConn is a net.Conn whose Write blocks until release, then fails.
type blockedWriteConn struct {
	net.Conn
	writing chan struct{}
	release chan struct{}
}

func (c *blockedWriteConn) Write([]byte) (int, error) {
	close(c.writing)
	<-c.release
	return 0, errors.New("connection reset")
}

func TestRawSocketPeerCloseDuringFailingWrite(t *testing.T) {
	conn, other := net.Pipe()
	defer other.Close()
	blocked := &blockedWriteConn{Conn: conn, writing: make(chan struct{}), release: make(chan struct{})}
	peer := xconn.NewRawSocketPeer(blocked, xconn.RawSocketPeerConfig{Serializer: transports.SerializerCbor})

	ok, err := peer.TryWrite([]byte("hello"))
	require.NoError(t, err)
	require.True(t, ok)
	<-blocked.writing

	// Close while the writer is stuck in a write that then fails: the writer closes the peer
	// itself, which must not deadlock with this Close waiting for the writer.
	closed := make(chan error, 1)
	go func() { closed <- peer.Close() }()
	time.Sleep(50 * time.Millisecond)
	close(blocked.release)

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close deadlocked with the failing writer")
	}
}
