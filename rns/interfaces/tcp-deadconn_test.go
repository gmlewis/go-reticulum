// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

package interfaces

import (
	"net"
	"sync"
	"testing"
	"time"
)

// deadConn is a net.Conn standing in for a TCP peer that has gone away, so a
// test can close the connection underneath a live client interface and count
// what that interface does about it. Write fails once the connection is closed
// — what a real socket whose peer has vanished reports — and every attempt is
// counted, so one write can be told apart from a storm of them.
type deadConn struct {
	mu       sync.Mutex
	writes   int
	closed   chan struct{}
	closeMux sync.Once
}

func newDeadConn() *deadConn {
	return &deadConn{closed: make(chan struct{})}
}

func (c *deadConn) Read(p []byte) (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}

func (c *deadConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	c.writes++
	c.mu.Unlock()
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
		return len(p), nil
	}
}

func (c *deadConn) Close() error {
	c.closeMux.Do(func() { close(c.closed) })
	return nil
}

func (c *deadConn) writeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

func (c *deadConn) LocalAddr() net.Addr                { return nil }
func (c *deadConn) RemoteAddr() net.Addr               { return nil }
func (c *deadConn) SetDeadline(t time.Time) error      { return nil }
func (c *deadConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *deadConn) SetWriteDeadline(t time.Time) error { return nil }

// blockingCloseConn is a net.Conn whose Close announces that it has been
// entered and then waits to be released. It gives a test the one window it
// cannot otherwise reach: the instant at which the interface has let go of its
// connection but has not finished taking itself down.
type blockingCloseConn struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingCloseConn() *blockingCloseConn {
	return &blockingCloseConn{entered: make(chan struct{}), release: make(chan struct{})}
}

func (c *blockingCloseConn) Read(p []byte) (int, error) { return 0, net.ErrClosed }

func (c *blockingCloseConn) Write(p []byte) (int, error) { return 0, net.ErrClosed }

func (c *blockingCloseConn) Close() error {
	c.once.Do(func() {
		close(c.entered)
		<-c.release
	})
	return nil
}

func (c *blockingCloseConn) LocalAddr() net.Addr                { return nil }
func (c *blockingCloseConn) RemoteAddr() net.Addr               { return nil }
func (c *blockingCloseConn) SetDeadline(t time.Time) error      { return nil }
func (c *blockingCloseConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *blockingCloseConn) SetWriteDeadline(t time.Time) error { return nil }

// downClientForTest builds the smallest live outbound client interface around a
// given connection: spawned, so no reconnect loop dials an empty target, and
// with a short write timeout so nothing in the test waits on a socket.
func downClientForTest(name string, conn net.Conn) *TCPClientInterface {
	return &TCPClientInterface{
		BaseInterface: NewBaseInterface(name, ModeFull, TCPBitrateGuess),
		conn:          conn,
		writeTimeout:  100 * time.Millisecond,
		spawned:       true,
		running:       1,
	}
}

// TestTCPClientInterface_ClosedConnDoesNotStormSends pins what happens when the
// connection underneath a live client interface is closed: the Send that
// discovers it fails, the interface goes down, and every Send after that fails
// fast instead of writing to the dead socket again.
//
// Send is reached from the transport's maintenance loop and every forwarding
// path, so an interface that kept reporting itself up and kept writing would
// turn one flapping peer into a stream of write errors on a socket that can
// never carry them.
func TestTCPClientInterface_ClosedConnDoesNotStormSends(t *testing.T) {
	t.Parallel()

	conn := newDeadConn()
	tci := downClientForTest("test-dead-conn", conn)

	// The peer goes away while the interface still believes it is up.
	if err := conn.Close(); err != nil {
		t.Fatalf("closing the peer's end: %v", err)
	}

	if err := tci.Send([]byte("first")); err == nil {
		t.Fatal("Send over a closed connection succeeded")
	}
	if tci.Status() {
		t.Fatal("the interface still reports itself up after its connection was closed: " +
			"the transport would keep routing through an interface that cannot carry a byte")
	}

	for range 8 {
		if err := tci.Send([]byte("later")); err == nil {
			t.Fatal("Send over an interface that is down succeeded")
		}
	}
	if got := conn.writeCount(); got != 1 {
		t.Fatalf("the dead connection saw %v writes, want 1: every Send after the first must "+
			"fail fast rather than storm a socket that is already closed", got)
	}
}

// TestTCPClientInterface_StatusNeverReportsUpWithoutItsConn pins the ordering
// that makes Status honest: an interface takes itself down before it lets go of
// its connection, so it is never reported as up at a moment when there is
// nothing left to send over.
//
// This is the order Python's TCPInterface keeps — it sets online = False and
// only then tears the socket down, just as it creates the socket and only then
// sets online = True — and the Go port keeps it for the same reason: Status is
// read without a lock, by the transport deciding which interfaces a path may
// use, so the order of the two writes is the only thing that decides what those
// readers see.
func TestTCPClientInterface_StatusNeverReportsUpWithoutItsConn(t *testing.T) {
	t.Parallel()

	conn := newBlockingCloseConn()
	tci := downClientForTest("test-atomic-down", conn)

	sent := make(chan error, 1)
	go func() { sent <- tci.Send([]byte("payload")) }()

	// Send has failed on the closed connection and failConn is now inside
	// conn.Close, held there until this test releases it.
	select {
	case <-conn.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Send never reached the connection's Close")
	}

	if tci.Status() {
		close(conn.release)
		_ = <-sent
		t.Fatal("the interface reports itself up at a moment when it has no connection to " +
			"send over: a transport that trusted this would route through an interface that " +
			"cannot carry a byte")
	}

	close(conn.release)
	select {
	case err := <-sent:
		if err == nil {
			t.Fatal("Send over a closed connection succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not return after the connection was released")
	}
}
