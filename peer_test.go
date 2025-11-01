package xconn_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
