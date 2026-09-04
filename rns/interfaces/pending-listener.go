// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"net"
	"sync"
	"time"
)

// pendingTCPListeners is a test-support seam for handing pre-bound listeners
// to the production bind sites below. The rns test suite reserves TCP ports
// for shared_instance_port / instance_control_port / listen_port by holding a
// probe listener open for the duration of each test (rns reserveTCPPort)
// instead of closing it and hoping the port can be rebound later: in between,
// a parallel test's listener bind or outgoing dial can claim the just-closed
// ephemeral port, which silently misclassifies the instance role
// (shared/standalone/connected) or fails a server bind — the
// TestRPCAuthAndGetEndpoints "expected shared instance" CI flake of
// 2026-09-04.
//
// A bind site consults PopPendingTCPListener before net.Listen and adopts the
// held listener when one is registered for its port. The registry is only
// ever populated by the test suite, so production behavior is unchanged
// (every Pop misses and the normal bind path runs). Entries are consumed at
// most once; listeners never adopted are closed by the reserving test's
// t.Cleanup via ReleasePendingTCPListener.
//
// While held, a probe listener sheds inbound connections (accept and
// immediately close) so a dial to a reserved-but-unbound port fails fast
// (EOF/reset) instead of hanging on a silently held socket — dialers that
// previously saw connection-refused must keep getting a prompt error.
//
// Keying by port is unambiguous: while a probe listener is held open, the
// kernel cannot hand the same port to another test's probe or dial, so no two
// live registrations can share a port within a process.
var pendingTCPListeners sync.Map // map[int]*pendingTCPProbe

// probeAcceptPoll bounds how long the shed loop blocks in Accept so
// PopPendingTCPListener's drain handshake completes promptly.
const probeAcceptPoll = 50 * time.Millisecond

type pendingTCPProbe struct {
	listener net.Listener
	stop     chan struct{}
	stopped  chan struct{}
}

// HoldPendingTCPListener registers a pre-bound listener for port so the next
// bind site that needs it can adopt it without rebinding. Test-support only.
func HoldPendingTCPListener(port int, l net.Listener) {
	p := &pendingTCPProbe{listener: l, stop: make(chan struct{}), stopped: make(chan struct{})}
	pendingTCPListeners.Store(port, p)
	go p.shedLoop()
}

// shedLoop accepts and immediately closes inbound connections until stopped.
func (p *pendingTCPProbe) shedLoop() {
	defer close(p.stopped)
	tl, canDeadline := p.listener.(*net.TCPListener)
	for {
		if canDeadline {
			_ = tl.SetDeadline(time.Now().Add(probeAcceptPoll))
		}
		conn, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.stop:
				return
			default:
			}
			if !canDeadline {
				return
			}
			continue // poll deadline elapsed; re-check stop and accept again
		}
		_ = conn.Close()
		select {
		case <-p.stop:
			return
		default:
		}
	}
}

// PopPendingTCPListener removes and returns the held listener for port, or
// nil when nothing is registered — the normal production case, in which the
// caller proceeds with its own net.Listen. The shed loop is fully drained
// before returning so it cannot close connections meant for the server that
// adopts the listener.
func PopPendingTCPListener(port int) net.Listener {
	v, ok := pendingTCPListeners.LoadAndDelete(port)
	if !ok {
		return nil
	}
	p := v.(*pendingTCPProbe)
	close(p.stop)
	<-p.stopped
	// shedLoop polled Accept with a deadline; the adopting server must not
	// inherit it or its own Accept loop would time out too.
	if tl, ok := p.listener.(*net.TCPListener); ok {
		_ = tl.SetDeadline(time.Time{})
	}
	return p.listener
}

// ReleasePendingTCPListener removes and closes the held listener for port, if
// it is still registered (an adopted listener is owned by the code that
// popped it and is left alone). Test-support only.
func ReleasePendingTCPListener(port int) {
	if v, ok := pendingTCPListeners.LoadAndDelete(port); ok {
		p := v.(*pendingTCPProbe)
		close(p.stop)
		_ = p.listener.Close()
		<-p.stopped
	}
}
