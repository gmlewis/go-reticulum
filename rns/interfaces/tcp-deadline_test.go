// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

package interfaces

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// deadlineBlockingConn is a net.Conn whose Write blocks until the most
// recently set write deadline elapses (then returns an error), or until the
// connection is closed. It simulates a half-open TCP peer that accepts a
// connection but never drains its receive buffer, so conn.Write would block
// for the OS retransmit timeout in the absence of a write deadline.
type deadlineBlockingConn struct {
	mu     sync.Mutex
	wr     time.Time
	closed chan struct{}
}

func (c *deadlineBlockingConn) Read(p []byte) (int, error)        { return 0, io.EOF }
func (c *deadlineBlockingConn) LocalAddr() net.Addr               { return nil }
func (c *deadlineBlockingConn) RemoteAddr() net.Addr              { return nil }
func (c *deadlineBlockingConn) SetDeadline(t time.Time) error     { return nil }
func (c *deadlineBlockingConn) SetReadDeadline(t time.Time) error { return nil }
func (c *deadlineBlockingConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.wr = t
	c.mu.Unlock()
	return nil
}
func (c *deadlineBlockingConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}
func (c *deadlineBlockingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	wr := c.wr
	c.mu.Unlock()
	if wr.IsZero() {
		select {
		case <-c.closed:
			return 0, errors.New("closed")
		case <-time.After(time.Hour):
			return 0, errors.New("timeout")
		}
	}
	select {
	case <-time.After(time.Until(wr)):
		return 0, errors.New("write deadline exceeded")
	case <-c.closed:
		return 0, errors.New("closed")
	}
}

// TestTCPClientInterface_SendWriteDeadlineUnblocks verifies that a Send to a
// half-open peer (one whose Write blocks) returns within the write deadline
// instead of blocking for the OS retransmit timeout, that the interface
// transitions down, and that the onDown hook fires once on that transition.
// This is the regression test for the transport wedge where a single stalled
// TCP interface blocked the maintenance loop and every readLoop indefinitely.
func TestTCPClientInterface_SendWriteDeadlineUnblocks(t *testing.T) {
	t.Parallel()

	conn := &deadlineBlockingConn{closed: make(chan struct{})}
	// spawned=true so failConn does not spawn a reconnectLoop dialing an
	// empty target for the duration of the test. writeTimeout is shortened
	// per interface (not via a package global) so this completes quickly.
	tci := &TCPClientInterface{
		BaseInterface: NewBaseInterface("test-blocked", ModeFull, TCPBitrateGuess),
		conn:          conn,
		writeTimeout:  100 * time.Millisecond,
		spawned:       true,
		running:       1,
	}

	onDownFired := make(chan struct{}, 1)
	tci.SetOnDown(func() {
		select {
		case onDownFired <- struct{}{}:
		default:
		}
	})

	start := time.Now()
	err := tci.Send([]byte("payload"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Send to a blocked peer should have failed with a write error")
	}
	// The write deadline must bound Send; without it Send would block for
	// the OS retransmit timeout and exceed the test's own deadline.
	if elapsed > 2*time.Second {
		t.Fatalf("Send blocked %v; write deadline did not fire", elapsed)
	}
	if tci.Status() {
		t.Fatal("interface should be down after a write deadline failure")
	}
	select {
	case <-onDownFired:
	default:
		t.Fatal("onDown hook should fire on the up->down transition")
	}

	// A subsequent Send on the now-down interface must fail fast rather
	// than block on a write deadline again.
	if err := tci.Send([]byte("again")); err == nil {
		t.Fatal("second Send on a down interface should fail fast")
	}
}
