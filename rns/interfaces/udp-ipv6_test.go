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

package interfaces

import (
	"net"
	"testing"
)

// reserveIPv6UDPPort returns a free IPv6 UDP port bound on ip, skipping the
// test where the host has no usable IPv6 address of that kind.
//
// The port is probed with the same network and address the interface is about
// to bind with (see UDPInterface.start), so what it returns is a port that was
// free for exactly that bind. It is then released, because the interfaces these
// tests build must perform their own bind: that bind is the whole point of the
// tests — hostPortAddr's bracketing of an IPv6 literal is the regression they
// exist to catch — so handing the interface a socket held by the test would
// make both pass vacuously. Releasing the probe returns the port to the
// kernel's ephemeral allocator, where a parallel test can claim it before the
// caller binds; callers therefore treat a lost reservation as expected and
// retry (see bindIPv6UDPInterfaceForTest).
func reserveIPv6UDPPort(t *testing.T, ip net.IP) int {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip, Port: 0})
	if err != nil {
		t.Skipf("IPv6 %v unavailable: %v", ip, err)
	}
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		_ = conn.Close()
		t.Fatalf("reserveIPv6UDPPort unexpected addr type: %T", conn.LocalAddr())
	}
	port := addr.Port
	if err := conn.Close(); err != nil {
		t.Fatalf("reserveIPv6UDPPort close: %v", err)
	}
	return port
}

// bindIPv6UDPInterfaceForTest builds and starts an IPv6 UDP interface on a
// freshly reserved port, retrying the whole scenario on a new port when the
// reservation was lost — the probe is released before the interface binds it,
// so under -race a parallel test's own reservation can take it in between, and
// that is expected rather than a failure.
//
// Any other error is the interface failing to start at all, which is the
// regression these tests exist for (":::port" from joining an IPv6 host and
// port by hand, which net.ResolveUDPAddr rejects with "too many colons in
// address"), and fails the test at once rather than being retried away.
func bindIPv6UDPInterfaceForTest(t *testing.T, name, listenIP, forwardIP string, forwardPort int) *UDPInterface {
	t.Helper()
	ip := net.ParseIP(listenIP)
	if ip == nil {
		t.Fatalf("bindIPv6UDPInterfaceForTest: %q is not an IP address", listenIP)
	}
	for range portReserveAttempts {
		listenPort := reserveIPv6UDPPort(t, ip)
		iface, err := NewUDPInterface(name, listenIP, listenPort, forwardIP, forwardPort, nil)
		if err == nil {
			t.Cleanup(func() { _ = iface.Detach() })
			return iface
		}
		if !isAddrInUse(err) {
			t.Fatalf("UDP interface %v on IPv6 %v: %v", name, listenIP, err)
		}
	}
	t.Fatalf("no reserved IPv6 port on %v stayed free over %v attempts", listenIP, portReserveAttempts)
	return nil
}

// TestNewUDPInterfaceBindsIPv6Literals verifies a UDPInterface configured with
// bare IPv6 literals can resolve both its listen coordinates and its forwarding
// target. Python passes these as (host, port) tuples, which is inherently
// IPv6-safe; joining them as "host:port" produces ":::PORT", which
// net.ResolveUDPAddr rejects with "too many colons in address" — so every IPv6
// UDP interface failed to start, while its Python counterpart worked.
func TestNewUDPInterfaceBindsIPv6Literals(t *testing.T) {
	t.Parallel()

	iface := bindIPv6UDPInterfaceForTest(t, "v6-udp", "::1", "::1", reserveIPv6UDPPort(t, net.IPv6loopback))

	if !iface.Status() {
		t.Fatal("UDP interface with IPv6 literals did not reach running state")
	}
}

// TestNewUDPInterfaceBindsIPv6Wildcard verifies the IPv6 wildcard bind address
// ("::", the IPv6 equivalent of 0.0.0.0) is accepted, since that is what an
// IPv6 UDP listener is normally configured with.
func TestNewUDPInterfaceBindsIPv6Wildcard(t *testing.T) {
	t.Parallel()

	iface := bindIPv6UDPInterfaceForTest(t, "v6-any", "::", "::1", 4242)

	if !iface.Status() {
		t.Fatal("UDP interface bound to the IPv6 wildcard did not reach running state")
	}
}
