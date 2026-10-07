package xconn

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// maxMsgSize bounds readMsg's allocation.
const maxMsgSize = 1 << 20 // 1 MiB

// safeGo runs f in a new goroutine, recovering any panic so a bug in one background task
// cannot take down the whole process.
func safeGo(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("recovered panic in goroutine: %v\n%s", r, debug.Stack())
			}
		}()
		f()
	}()
}

// writeMsg writes v as length-prefixed JSON.
func writeMsg(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(b))) //nolint:gosec
	if _, err := w.Write(length[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// readMsg reads a message written by writeMsg into v.
func readMsg(r io.Reader, v any) error {
	var length [4]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(length[:])
	if n > maxMsgSize {
		return fmt.Errorf("message too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return json.Unmarshal(buf, v)
}

// spliceConns copies between a and b until either side ends, then closes both.
func spliceConns(a, b net.Conn) {
	var closeOnce sync.Once
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = a.Close()
			_ = b.Close()
			// Closing a multiplexed stream only ends its write side: don't wait for the peer.
			_ = a.SetReadDeadline(time.Now())
			_ = b.SetReadDeadline(time.Now())
		})
	}
	done := make(chan struct{})
	safeGo(func() {
		_, _ = io.Copy(a, b)
		closeBoth()
		close(done)
	})
	_, _ = io.Copy(b, a)
	closeBoth()
	<-done
}

// deliverUnlessClosed sends v on ch, giving up only if ch is full and closed has ended. A
// plain select on both could drop a message that arrives just as its channel closes.
func deliverUnlessClosed[T any](ch chan<- T, v T, closed <-chan struct{}) {
	select {
	case ch <- v:
		return
	default:
	}
	select {
	case ch <- v:
	case <-closed:
	}
}
