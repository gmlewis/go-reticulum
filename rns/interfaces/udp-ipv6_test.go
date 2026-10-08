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

// reserveIPv6UDPPort returns a free UDP port on the IPv6 loopback, skipping the
// test where the host has no usable IPv6 loopback.
func reserveIPv6UDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenUDP("udp6", &net.UDPAddr{IP: net.IPv6loopback, Port: 0})
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
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

// TestNewUDPInterfaceBindsIPv6Literals verifies a UDPInterface configured with
// bare IPv6 literals can resolve both its listen coordinates and its forwarding
// target. Python passes these as (host, port) tuples, which is inherently
// IPv6-safe; joining them as "host:port" produces ":::PORT", which
// net.ResolveUDPAddr rejects with "too many colons in address" — so every IPv6
// UDP interface failed to start, while its Python counterpart worked.
func TestNewUDPInterfaceBindsIPv6Literals(t *testing.T) {
	t.Parallel()

	listenPort := reserveIPv6UDPPort(t)
	forwardPort := reserveIPv6UDPPort(t)

	iface := mustTestNewUDPInterface(t, "v6-udp", "::1", listenPort, "::1", forwardPort, nil)
	t.Cleanup(func() { _ = iface.Detach() })

	if !iface.Status() {
		t.Fatal("UDP interface with IPv6 literals did not reach running state")
	}
}

// TestNewUDPInterfaceBindsIPv6Wildcard verifies the IPv6 wildcard bind address
// ("::", the IPv6 equivalent of 0.0.0.0) is accepted, since that is what an
// IPv6 UDP listener is normally configured with.
func TestNewUDPInterfaceBindsIPv6Wildcard(t *testing.T) {
	t.Parallel()

	listenPort := reserveIPv6UDPPort(t)

	iface := mustTestNewUDPInterface(t, "v6-any", "::", listenPort, "::1", 4242, nil)
	t.Cleanup(func() { _ = iface.Detach() })

	if !iface.Status() {
		t.Fatal("UDP interface bound to the IPv6 wildcard did not reach running state")
	}
}
