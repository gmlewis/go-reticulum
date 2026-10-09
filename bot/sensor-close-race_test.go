// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package bot

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// blockingDial hands a dial that parks until the test releases it, which is how the window
// between "the reader decided to reconnect" and "the connection is published" is held open
// rather than raced for.
//
// It hands back a REAL connection — one end of a net.Pipe whose other end nobody ever writes to
// — because that is what makes the difference visible. A dial that fails is cleaned up by the
// ordinary error path either way; a dial that SUCCEEDS is the one that gets published, and a
// published connection that Close has already stopped looking at is the one the reader blocks
// on forever.
type blockingDial struct {
	entered chan struct{}
	release chan struct{}
	conn    net.Conn
}

func newBlockingDial(conn net.Conn) *blockingDial {
	return &blockingDial{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		conn:    conn,
	}
}

// dial blocks until release is closed, then hands over the connection it was given.
func (d *blockingDial) dial(network, address string) (net.Conn, error) {
	select {
	case d.entered <- struct{}{}:
	default:
	}
	<-d.release
	return d.conn, nil
}

// TestClosingWhileADialIsInFlightDoesNotStrandTheReader is the regression test for a race that
// hung a package for its whole timeout while passing when run alone.
//
// Read dials a replacement connection whenever its stream ends, and it never used to ask whether
// the socket had been closed meanwhile. So a read that slipped past the backoff an instant
// before Close landed would dial a connection *after* Close had run its once-only body, publish
// it, and then block on it forever — because Close had already fired and would never close it.
// The scan goroutine never exited, and the Close that waits for it never returned.
//
// The dial is held open here, so the close provably lands inside it rather than probably.
func TestClosingWhileADialIsInFlightDoesNotStrandTheReader(t *testing.T) {
	t.Parallel()

	// One end is handed to the reader; the other is held here and never written to, so the
	// read on it blocks until something closes it. Only the fix closes it.
	held, handed := net.Pipe()
	t.Cleanup(func() { _ = held.Close() })

	socket := newSensorSocket(sensorEndpoint{network: "tcp", address: "127.0.0.1:1"})
	injected := newBlockingDial(handed)
	socket.dial = injected.dial
	// No real waiting: the reconnect delay is not what is under test.
	socket.sleep = func(time.Duration) bool { return true }

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 64)
		for {
			if _, err := socket.Read(buf); err != nil {
				return
			}
		}
	}()

	// Wait until the reader is inside the dial, so the close cannot miss it.
	select {
	case <-injected.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the reader never reached the dial")
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		_ = socket.Close()
	}()

	// Release the dial: the connection it would have produced must be refused, not published.
	close(injected.release)

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return while a dial was in flight; it is waiting on something " +
			"that will never finish")
	}

	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the reader never returned after Close; it is blocked on a connection that " +
			"Close will never close")
	}

	// A refused dial must leave nothing behind, closed or otherwise.
	socket.mu.Lock()
	leftover := socket.conn
	socket.mu.Unlock()
	if leftover != nil {
		t.Errorf("a connection dialled after Close was published anyway: %v", leftover)
	}

	// And the connection itself must have been released, not merely forgotten.
	if _, err := handed.Write([]byte("x")); err == nil {
		t.Error("the connection dialled after Close was still open; nothing closed it")
	}
}

// TestReadReturnsEndOfInputOnceClosed asserts the ordinary case beside the racy one: a stream
// that has been closed reports an end of input at once and never dials again.
func TestReadReturnsEndOfInputOnceClosed(t *testing.T) {
	t.Parallel()

	socket := newSensorSocket(sensorEndpoint{network: "tcp", address: "127.0.0.1:1"})
	dials := 0
	socket.dial = func(network, address string) (net.Conn, error) {
		dials++
		return nil, errors.New("no peer")
	}
	socket.sleep = func(time.Duration) bool { return true }

	if err := socket.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := socket.Read(make([]byte, 8)); !errors.Is(err, io.EOF) {
		t.Fatalf("Read after Close = %v, want io.EOF", err)
	}
	if dials != 0 {
		t.Errorf("Read dialled %v time(s) after Close, want none", dials)
	}
}
