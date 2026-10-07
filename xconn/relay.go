package xconn

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/pion/webrtc/v4"
	log "github.com/sirupsen/logrus"
)

// RelayKind says whether a relayed connection carries a byte stream or WebRTC messages.
type RelayKind string

const (
	// RelayKindStream: the rest of the connection is a raw stream's bytes, untouched.
	RelayKindStream RelayKind = "stream"
	// RelayKindWebRTC: the rest of the connection is a sequence of relay frames (see
	// WriteRelayFrame), one per data channel message, starting with the channel's first.
	RelayKindWebRTC RelayKind = "webrtc"
)

// RelayHeader is the first message xconn writes, as length-prefixed JSON (a 4-byte
// big-endian length, then the JSON), on every connection it opens to App.StreamSocket.
type RelayHeader struct {
	Kind RelayKind `json:"kind"`
	// Label is the data channel's label, for RelayKindWebRTC.
	Label string `json:"label,omitempty"`
}

// ReadRelayHeader reads the RelayHeader off a connection accepted on App.StreamSocket.
func ReadRelayHeader(r io.Reader) (RelayHeader, error) {
	var h RelayHeader
	err := readMsg(r, &h)
	return h, err
}

const (
	relayFrameBinary byte = 0
	relayFrameText   byte = 1

	// maxRelayFrameSize bounds ReadRelayFrame's allocation.
	maxRelayFrameSize = 1 << 20 // 1 MiB

	// relayBufferedHigh/Low bound how much data a relayed channel lets pile up in the
	// real data channel's send buffer.
	relayBufferedHigh = 512 * 1024
	relayBufferedLow  = 256 * 1024
)

// WriteRelayFrame writes one data channel message (and whether it was sent as text or
// binary) as one frame on a RelayKindWebRTC connection: a kind byte, a 4-byte big-endian
// length, then the data.
func WriteRelayFrame(w io.Writer, data []byte, isText bool) error {
	var header [5]byte
	header[0] = relayFrameBinary
	if isText {
		header[0] = relayFrameText
	}
	binary.BigEndian.PutUint32(header[1:], uint32(len(data))) //nolint:gosec
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// ReadRelayFrame reads one frame written by WriteRelayFrame.
func ReadRelayFrame(r io.Reader) (data []byte, isText bool, err error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, false, err
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n > maxRelayFrameSize {
		return nil, false, fmt.Errorf("relay frame too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, false, err
	}
	return buf, header[0] == relayFrameText, nil
}

// RelayStream splices a raw stream from a remote client (a QUIC or WebTransport stream), untouched,
// to the app's stream socket.
func RelayStream(stream net.Conn, streamSocket string) {
	defer stream.Close()

	conn, err := net.Dial(networkUnix, streamSocket)
	if err != nil {
		log.Printf("relay: failed to dial app stream socket: %v", err)
		return
	}
	defer conn.Close()

	if err := writeMsg(conn, RelayHeader{Kind: RelayKindStream}); err != nil {
		return
	}

	// Ends, closing both, as soon as either side does: the app closing its end is how
	// a session finishes, and the client only learns of it when the stream closes.
	spliceConns(conn, stream)
}

// handleDataChannel is the callback wired to the WebRTC provider's OnDataChannel: it relays
// every non-WAMP data channel, with its first message, to the app's stream socket.
//
// The provider invokes this synchronously from the channel's own message dispatch goroutine
// (it has to: it's the one sniffing the first message), so the relay work must happen on its
// own goroutine -- relayDataChannel blocks until the channel closes, and until this callback
// returns, the provider can't dispatch this channel's next message to the OnMessage handler
// relayDataChannel registers.
func handleDataChannel(streamSocket string) func(sessionID string, channel *webrtc.DataChannel,
	firstMessage []byte) {
	return func(_ string, channel *webrtc.DataChannel, firstMessage []byte) {
		safeGo(func() {
			conn, err := net.Dial(networkUnix, streamSocket)
			if err != nil {
				log.Printf("relay: failed to dial app stream socket: %v", err)
				_ = channel.Close()
				return
			}

			if err := writeMsg(conn, RelayHeader{Kind: RelayKindWebRTC, Label: channel.Label()}); err != nil {
				_ = channel.Close()
				_ = conn.Close()
				return
			}

			relayDataChannel(channel, conn, firstMessage)
		})
	}
}

// relayDataChannel splices channel's messages to/from conn as relay frames in both
// directions, respecting the channel's send backpressure. firstMessage is relayed as the
// first frame.
func relayDataChannel(channel *webrtc.DataChannel, conn net.Conn, firstMessage []byte) {
	defer conn.Close()

	if err := WriteRelayFrame(conn, firstMessage, true); err != nil {
		_ = channel.Close()
		return
	}

	closed := make(chan struct{})
	var closeOnce sync.Once
	signalClosed := func() { closeOnce.Do(func() { close(closed) }) }
	channel.OnClose(signalClosed)
	channel.OnError(func(error) { signalClosed() })

	msgCh := make(chan webrtc.DataChannelMessage, 32)
	channel.OnMessage(func(msg webrtc.DataChannelMessage) {
		deliverUnlessClosed(msgCh, msg, closed)
	})

	// channel -> conn
	safeGo(func() {
		for {
			select {
			case msg := <-msgCh:
				if err := WriteRelayFrame(conn, msg.Data, msg.IsString); err != nil {
					_ = channel.Close()
					return
				}
			case <-closed:
				// Relay what arrived before the close, then close conn too.
				for {
					select {
					case msg := <-msgCh:
						if WriteRelayFrame(conn, msg.Data, msg.IsString) != nil {
							_ = conn.Close()
							return
						}
					default:
						_ = conn.Close()
						return
					}
				}
			}
		}
	})

	// conn -> channel, pausing whenever the channel's own send buffer is already full
	// rather than queuing unboundedly on top of it.
	sendReady := make(chan struct{}, 1)
	channel.SetBufferedAmountLowThreshold(relayBufferedLow)
	channel.OnBufferedAmountLow(func() {
		select {
		case sendReady <- struct{}{}:
		default:
		}
	})

	for {
		data, isText, err := ReadRelayFrame(conn)
		if err != nil {
			_ = channel.Close()
			return
		}

		for channel.BufferedAmount()+uint64(len(data)) > relayBufferedHigh {
			select {
			case <-sendReady:
			case <-closed:
				return
			}
		}

		if isText {
			err = channel.SendText(string(data))
		} else {
			err = channel.Send(data)
		}
		if err != nil {
			return
		}
	}
}
