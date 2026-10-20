// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"errors"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// reserveTCPPort returns a loopback TCP port for a test and keeps it bound
// until the code under test adopts the held listener.
//
// The returned port is not merely probed and released: the probe listener stays
// open and is handed to the next bind site that needs it (see
// pending-listener.go — the TCP, Backbone, I2P and Local server constructors
// all adopt a held listener instead of calling net.Listen themselves). A closed
// probe would hand the just-released ephemeral port back to the kernel, where a
// parallel test's own reserveTCPPort, listener bind, or outgoing dial could
// claim it before the caller binds. While the probe is held, the port cannot
// leave this test's control, and the probe sheds inbound connections (accept
// then close) so a premature dial still fails promptly instead of hanging.
//
// A held listener that no bind site adopts is closed by the test cleanup below.
// Tests that must bind the port themselves, or hand it to a subprocess that
// cannot adopt a Go listener, need reserveUnboundTCPPort instead.
func reserveTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveTCPPort: %v", err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		_ = l.Close()
		t.Fatalf("reserveTCPPort unexpected addr type: %T", l.Addr())
	}
	port := addr.Port
	HoldPendingTCPListener(port, l)
	t.Cleanup(func() { ReleasePendingTCPListener(port) })
	return port
}

// reserveHeldIPv6TCPPort returns an IPv6 loopback TCP port for a test and keeps
// its listener bound until the code under test adopts it, the IPv6 counterpart
// of reserveTCPPort (see pendingTCPListeners). An interface that has to perform
// its own bind cannot use it — reserveIPv6LoopbackTCPPort leaves the port free
// for that, and its callers retry instead.
func reserveHeldIPv6TCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		_ = l.Close()
		t.Fatalf("reserveHeldIPv6TCPPort unexpected addr type: %T", l.Addr())
	}
	port := addr.Port
	HoldPendingTCPListener(port, l)
	t.Cleanup(func() { ReleasePendingTCPListener(port) })
	return port
}

// reserveIPv6LoopbackTCPPort returns a free TCP port on the IPv6 loopback that is
// left free for the caller to bind, for the one case reserveHeldIPv6TCPPort
// cannot serve: a test that asserts on the bind itself, which adopting a held
// listener would skip. The port therefore leaves this test's control as soon as
// it is returned, so callers must treat losing it as expected and retry the
// whole bind on a fresh port (see portReserveAttempts) rather than failing.
func reserveIPv6LoopbackTCPPort(t *testing.T) int {
	t.Helper()
	probe, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	addr, ok := probe.Addr().(*net.TCPAddr)
	if !ok {
		_ = probe.Close()
		t.Fatalf("reserveIPv6LoopbackTCPPort unexpected addr type: %T", probe.Addr())
	}
	port := addr.Port
	if err := probe.Close(); err != nil {
		t.Fatalf("reserveIPv6LoopbackTCPPort close: %v", err)
	}
	return port
}

// reserveUnboundTCPPort returns a loopback TCP port that is left free for the
// caller to bind later, for the two cases reserveTCPPort cannot serve: a test
// that binds the port itself, and a port handed to a Python subprocess, which
// cannot adopt a held Go listener. The port therefore leaves this test's
// control as soon as it is returned, so callers must treat losing it as
// expected and retry the whole bind on a fresh port (see
// TestTCPClientReconnectFiresOnConnect and startPythonEchoOnReservedPort)
// rather than failing the test.
func reserveUnboundTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveUnboundTCPPort: %v", err)
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		_ = l.Close()
		t.Fatalf("reserveUnboundTCPPort unexpected addr type: %T", l.Addr())
	}
	port := addr.Port
	if err := l.Close(); err != nil {
		t.Fatalf("reserveUnboundTCPPort close: %v", err)
	}
	return port
}

// waitSignal waits for a test synchronization channel, failing with msg after
// timeout. Test synchronization that has no bound turns a regression into a
// package-wide hang that only surfaces minutes later as a go test timeout, so
// every bare receive on such a channel goes through here.
func waitSignal(t *testing.T, ch <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatal(msg)
	}
}

// portReserveAttempts bounds how many times a test retries a scenario whose
// reserved port has to stay free until the caller binds it (see
// reserveUnboundTCPPort). Losing such a port to a parallel test is expected, so
// the scenario is retried on a fresh port instead of failing the test.
const portReserveAttempts = 5

// isAddrInUse reports whether err is the kernel refusing a bind because the
// port was taken. It is what tells a lost port reservation — which a caller
// answers by retrying on a fresh port — apart from an address the code under
// test could not derive at all, which is the failure the caller must report.
func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// allocateUDPPortPair returns two loopback UDP ports for interfaces that forward
// to each other, holding both sockets until the interfaces adopt them.
func allocateUDPPortPair(t *testing.T) (int, int) {
	t.Helper()
	return reserveHeldUDPPort(t), reserveHeldUDPPort(t)
}

// reserveHeldUDPPort returns a loopback UDP port for a test and keeps it bound
// until the code under test adopts the held socket, the UDP counterpart of
// reserveTCPPort (see pendingUDPSocket). The held socket is handed to the next
// UDPInterface that binds that port, so the port cannot be handed to a parallel
// test's own reservation in between. A held socket that no interface adopts is
// closed by the test cleanup below. Tests that need the port to stay free for
// another process need reserveUnboundUDPPort instead.
func reserveHeldUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("reserveHeldUDPPort: %v", err)
	}
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		_ = conn.Close()
		t.Fatalf("reserveHeldUDPPort unexpected addr type: %T", conn.LocalAddr())
	}
	port := addr.Port
	HoldPendingUDPSocket(port, conn)
	t.Cleanup(func() { ReleasePendingUDPSocket(port) })
	return port
}

// reserveUnboundUDPPort returns a loopback UDP port that is left free for
// another process to bind, for the one case reserveHeldUDPPort cannot serve: a
// port handed to a Python subprocess, which cannot adopt a held Go socket. The
// port is therefore reserved but not held, so callers must treat losing it as
// expected and retry the whole start on a fresh port (see
// startPythonEchoOnReservedUDPPort) rather than failing the test.
func reserveUnboundUDPPort(t *testing.T) int {
	t.Helper()
	return testutils.ReserveUDPPort(t)
}

// waitUntil polls cond every few milliseconds until it returns true or timeout
// elapses. It returns the final cond() value. Use it in place of a fixed
// time.Sleep before an async assertion so the test waits exactly as long as
// needed and never fatals merely because a fixed delay was too short under
// scheduler load.
func waitUntil(timeout time.Duration, cond func() bool) bool {
	return testutils.PollUntil(timeout, cond)
}

// waitForIfaceRunning polls a client interface's Status() until it reports
// running (connection established) or timeout elapses, then falls back to a
// short fixed wait for interfaces that do not expose Status().
func waitForIfaceRunning(t *testing.T, iface Interface, timeout time.Duration) {
	t.Helper()
	type runner interface{ Status() bool }
	r, ok := iface.(runner)
	if !ok {
		// No Status() to poll; fall back to a short fixed wait.
		time.Sleep(100 * time.Millisecond)
		return
	}
	if !waitUntil(timeout, r.Status) {
		t.Fatalf("interface did not report running within %v", timeout)
	}
}
