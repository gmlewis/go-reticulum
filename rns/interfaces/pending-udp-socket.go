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

// pendingUDPSockets is the UDP counterpart of pendingTCPListeners (see
// pending-listener.go): a test-support seam for handing a socket that is
// already bound to the production bind site in UDPInterface.start.
//
// The rns/interfaces UDP tests reserve a pair of loopback ports for two
// interfaces that forward to each other. Reserving a port by binding :0 and
// closing the probe socket hands it straight back to the kernel's ephemeral
// allocator, which under -race -count=5 hands the same just-released port to a
// parallel instance's own reservation: both instances then bind it and the
// second fails with "listen udp 127.0.0.1:<port>: bind: address already in use"
// (TestUDPInterface). A socket that stays bound cannot be handed out again, so
// the reserving test keeps the port until an interface adopts it.
//
// A bind site consults PopPendingUDPSocket before net.ListenUDP and adopts the
// held socket when one is registered for its port. The registry is only ever
// populated by the test suite, so production behavior is unchanged (every Pop
// misses and the normal bind path runs). Entries are consumed at most once;
// sockets never adopted are closed by the reserving test's t.Cleanup via
// ReleasePendingUDPSocket.
//
// Keying by port is unambiguous while a socket is held: the kernel cannot hand
// the same UDP port to another probe.
var pendingUDPSockets sync.Map // map[int]*net.UDPConn

// HoldPendingUDPSocket registers a bound UDP socket for port so the next bind
// site that needs it can adopt it without rebinding. Test-support only.
func HoldPendingUDPSocket(port int, conn *net.UDPConn) {
	pendingUDPSockets.Store(port, conn)
}

// PopPendingUDPSocket removes and returns the held UDP socket for port, or nil
// when nothing is registered — the normal production case, in which the caller
// proceeds with its own net.ListenUDP. Datagrams that arrived while the socket
// was held are discarded first, so an interface that adopts the socket never
// sees traffic that was not addressed to it.
func PopPendingUDPSocket(port int) *net.UDPConn {
	v, ok := pendingUDPSockets.LoadAndDelete(port)
	if !ok {
		return nil
	}
	conn := v.(*net.UDPConn)
	drainPendingUDP(conn)
	return conn
}

// ReleasePendingUDPSocket removes and closes the held UDP socket for port, if it
// is still registered (an adopted socket is owned by the code that popped it and
// is left alone). Test-support only.
func ReleasePendingUDPSocket(port int) {
	if v, ok := pendingUDPSockets.LoadAndDelete(port); ok {
		_ = v.(*net.UDPConn).Close()
	}
}

// pendingUDPDrainWindow bounds how long drainPendingUDP blocks waiting for one
// more queued datagram. Queued datagrams are readable immediately, so the window
// only ever bounds the final read that finds the socket empty.
const pendingUDPDrainWindow = time.Millisecond

// drainPendingUDP discards datagrams that arrived while the socket was held.
func drainPendingUDP(conn *net.UDPConn) {
	if err := conn.SetReadDeadline(time.Now().Add(pendingUDPDrainWindow)); err != nil {
		return
	}
	buf := make([]byte, 2048)
	for {
		if _, _, err := conn.ReadFromUDP(buf); err != nil {
			break
		}
	}
	// The adopting interface must not inherit the drain deadline or its read
	// loop would time out too.
	_ = conn.SetReadDeadline(time.Time{})
}
