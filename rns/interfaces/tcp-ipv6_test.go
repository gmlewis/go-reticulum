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
// IPv4 and host names pass through untouched.
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
		// would produce "[[::]]:port", which net rejects with "missing port in address".
		// The net.SplitHostPort check below is what catches it.
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

// TestTCPConcatHostPortIsUndialable documents why hostPortAddr must bracket: a
// bare "host:port" concatenation of an IPv6 literal is rejected by the net
// package before any I/O happens.
func TestTCPConcatHostPortIsUndialable(t *testing.T) {
	t.Parallel()

	naive := fmt.Sprintf("%v:%v", "::1", 4242)
	if _, err := net.Dial("tcp", naive); err == nil {
		t.Fatalf("net.Dial(%q) unexpectedly succeeded", naive)
	}
}

// TestTCPClientInterfaceConnectsToIPv6Literal verifies that a
// TCPClientInterface whose target is a bare IPv6 literal reaches running state;
// a hand-built "::1:PORT" dial address is rejected with "too many colons in
// address" and would leave the interface permanently down on an IPv6-only hub.
func TestTCPClientInterfaceConnectsToIPv6Literal(t *testing.T) {
	t.Parallel()

	// The port is held by a listener until the server adopts it (see
	// reserveHeldIPv6TCPPort): this test is about the *client* reaching an IPv6
	// literal, and the literal is still resolved for real on the client's dial.
	// Releasing a probe would let a parallel test claim the port and fail the
	// bind with "address already in use".
	port := reserveHeldIPv6TCPPort(t)

	server := mustTestNewTCPServerInterface(t, "v6-server", "::1", port, func([]byte, Interface) {})
	t.Cleanup(func() { _ = server.Detach() })

	client := mustTestNewTCPClientInterface(t, "v6-client", "::1", port, false, nil)
	t.Cleanup(func() { _ = client.Detach() })

	waitForIfaceRunning(t, client, 2*time.Second)
}

// TestTCPServerInterfaceBindsAnAlreadyBracketedIPv6Literal verifies that a config
// file writing the standard IPv6 notation — `listen_ip = [::]` — produces a hub
// that binds, listens and accepts. It asserts on the code's own behaviour, not
// on a formatted string: bracketing the literal a second time yields "[[::]]:port",
// which net refuses with "missing port in address", and the hub would never bind.
func TestTCPServerInterfaceBindsAnAlreadyBracketedIPv6Literal(t *testing.T) {
	t.Parallel()

	for _, bindIP := range []string{"[::1]", "::1"} {
		// Deliberately NOT parallel: both cases bind an IPv6 loopback port, and
		// running them together would put them in each other's way rather than
		// exercise the bracket.
		t.Run(bindIP, func(t *testing.T) {
			// The port is probed and then released, rather than held and adopted
			// the way reserveTCPPort holds one, because the server has to perform
			// the bind for this test to mean anything: the second bracket is
			// refused inside net.Listen, so adopting a listener this test had
			// already bound would skip the code path being guarded. A parallel
			// test can therefore claim the released port first; that is expected
			// and the scenario is retried on a fresh port rather than failed.
			for range portReserveAttempts {
				port := reserveIPv6LoopbackTCPPort(t)
				if bracketedIPv6ServerBindsAndAccepts(t, bindIP, port) {
					return
				}
			}
			t.Fatalf("no reserved IPv6 port stayed free over %v attempts", portReserveAttempts)
		})
	}
}

// bracketedIPv6ServerBindsAndAccepts binds a hub on bindIP:port and verifies it
// accepts a connection on the address derived from that bindIP. It reports false
// when the port reservation was lost (the caller retries on a fresh port), and
// fails the test for any other error — the double bracket, whose message names
// neither the bracket nor the config file that wrote it.
func bracketedIPv6ServerBindsAndAccepts(t *testing.T, bindIP string, port int) bool {
	t.Helper()
	server, err := NewTCPServerInterface("bracketed-server", bindIP, port, func([]byte, Interface) {}, nil)
	if err != nil {
		if isAddrInUse(err) {
			return false
		}
		t.Fatalf("a hub configured with listen_ip = %v could not bind: %v\n"+
			"hostPortAddr(%q, %v) = %q; the fleet's hub wrote [::] and was refused for days",
			bindIP, err, bindIP, port, hostPortAddr(bindIP, port))
	}
	defer func() { _ = server.Detach() }()

	// Binding is not enough: the listener has to accept, or the hub is up on
	// paper only.
	conn, err := net.DialTimeout("tcp", hostPortAddr(bindIP, port), 2*time.Second)
	if err != nil {
		t.Fatalf("nothing accepted on the address derived from listen_ip = %v: %v", bindIP, err)
	}
	_ = conn.Close()
	return true
}
