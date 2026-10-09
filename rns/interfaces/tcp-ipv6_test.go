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
	"fmt"
	"net"
	"testing"
	"time"
)

// TestHostPortAddrBracketsIPv6Literals pins the contract of hostPortAddr: an
// IPv6 literal must be bracketed so the result is a valid net address, while
// IPv4 and host names pass through untouched. The helper previously served only
// the __str__/hash rendering, which is why the bug it guards against went
// unnoticed: connect() built its dial address by hand instead.
func TestHostPortAddrBracketsIPv6Literals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		port int
		want string
	}{
		{"ipv4", "192.168.1.207", 4242, "192.168.1.207:4242"},
		{"hostname", "go-nomadnet.duckdns.org", 4242, "go-nomadnet.duckdns.org:4242"},
		{"ipv6 full", "2603:900b:3300:a::1be9", 4242, "[2603:900b:3300:a::1be9]:4242"},
		{"ipv6 loopback", "::1", 4242, "[::1]:4242"},
		{"ipv6 unspecified", "::", 0, "[::]:0"},
		// A literal that arrives ALREADY bracketed is how the standard IPv6 notation is
		// written, and how a config file naturally writes it. Bracketing it a second time
		// produced "[[::]]:port", which net rejects with "missing port in address" — a message
		// that names neither the bracket nor the file it came from.
		//
		// That one case cost a fleet its hub. A hub configured with listen_ip = [::] failed to
		// bind, gornsd logged the error once and skipped the interface, and every client in the
		// fleet was refused while the operator could see the service active and the config
		// correct. The net.SplitHostPort check below is what catches it.
		{"ipv6 unspecified already bracketed", "[::]", 4242, "[::]:4242"},
		{"ipv6 loopback already bracketed", "[::1]", 4242, "[::1]:4242"},
		{"ipv6 full already bracketed", "[2603:900b:3300:a::1be9]", 4242, "[2603:900b:3300:a::1be9]:4242"},
		{"ipv6 doubly bracketed", "[[::]]", 4242, "[::]:4242"},
		{"ipv4 already bracketed", "[192.168.1.207]", 4242, "192.168.1.207:4242"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := hostPortAddr(tc.host, tc.port)
			if got != tc.want {
				t.Fatalf("hostPortAddr(%q, %v) = %q, want %q", tc.host, tc.port, got, tc.want)
			}
			// The whole point of the brackets is that the net package can
			// parse the result, which is what net.Dial and net.Listen require.
			if _, _, err := net.SplitHostPort(got); err != nil {
				t.Errorf("net.SplitHostPort(%q) = %v, want a parseable address", got, err)
			}
		})
	}
}

// TestTCPConcatHostPortIsUndialable documents the failure the IPv6 fix removes:
// a bare "host:port" concatenation of an IPv6 literal is rejected by the net
// package before any I/O happens, so every IPv6 TCP interface was dead.
func TestTCPConcatHostPortIsUndialable(t *testing.T) {
	t.Parallel()

	naive := fmt.Sprintf("%v:%v", "::1", 4242)
	if _, err := net.Dial("tcp", naive); err == nil {
		t.Fatalf("net.Dial(%q) unexpectedly succeeded", naive)
	}
}

// TestTCPClientInterfaceConnectsToIPv6Literal is the regression test: a
// TCPClientInterface whose target is a bare IPv6 literal must reach running
// state. Before the fix, connect() dialled "::1:PORT" and failed with
// "dial tcp: address ::1:PORT: too many colons in address", leaving the
// interface permanently down on an IPv6-only hub.
func TestTCPClientInterfaceConnectsToIPv6Literal(t *testing.T) {
	t.Parallel()

	// Reserve a free port on the IPv6 loopback, then hand it to the server.
	probe, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	port, ok := probe.Addr().(*net.TCPAddr)
	if !ok {
		_ = probe.Close()
		t.Fatalf("unexpected listener addr type: %T", probe.Addr())
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}

	server := mustTestNewTCPServerInterface(t, "v6-server", "::1", port.Port, func([]byte, Interface) {})
	t.Cleanup(func() { _ = server.Detach() })

	client := mustTestNewTCPClientInterface(t, "v6-client", "::1", port.Port, false, nil)
	t.Cleanup(func() { _ = client.Detach() })

	waitForIfaceRunning(t, client, 2*time.Second)
}

// TestTCPServerInterfaceBindsAnAlreadyBracketedIPv6Literal is the guard that must never be
// deleted, and it is deliberately behavioural rather than a formatting check.
//
// A config file writing the standard IPv6 notation — `listen_ip = [::]` — must produce a hub
// that binds, listens and accepts. Bracketing that literal a second time yields "[[::]]:port",
// which net refuses with "missing port in address", a message that names neither the bracket
// nor the file. gornsd then logs the failure once, skips the interface, and goes on reporting
// itself healthy, so the operator sees an active service and a correct-looking config while
// every client in the fleet is refused. That is exactly what took a whole network down, and it
// happened after an earlier repair had been made by editing the config instead of the code —
// which is why this asserts on the code's own behaviour and never on what a config happens to
// say today.
func TestTCPServerInterfaceBindsAnAlreadyBracketedIPv6Literal(t *testing.T) {
	t.Parallel()

	// Reserve a free port on the IPv6 loopback.
	probe, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	addr, ok := probe.Addr().(*net.TCPAddr)
	if !ok {
		_ = probe.Close()
		t.Fatalf("unexpected listener addr type: %T", probe.Addr())
	}
	port := addr.Port
	if err := probe.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}

	for _, bindIP := range []string{"[::1]", "::1"} {
		// Deliberately NOT parallel: both cases bind the same reserved port, and the second
		// would race the first for it.
		t.Run(bindIP, func(t *testing.T) {
			server, err := NewTCPServerInterface("bracketed-server", bindIP, port, func([]byte, Interface) {}, nil)
			if err != nil {
				t.Fatalf("a hub configured with listen_ip = %v could not bind: %v\n"+
					"hostPortAddr(%q, %v) = %q; the fleet's hub wrote [::] and was refused for days",
					bindIP, err, bindIP, port, hostPortAddr(bindIP, port))
			}
			defer func() { _ = server.Detach() }()

			// Binding is not enough: the listener has to accept, or the hub is up on paper
			// exactly as it was while the fleet was down.
			conn, err := net.DialTimeout("tcp", hostPortAddr(bindIP, port), 2*time.Second)
			if err != nil {
				t.Fatalf("nothing accepted on the address derived from listen_ip = %v: %v", bindIP, err)
			}
			_ = conn.Close()
		})
	}
}
