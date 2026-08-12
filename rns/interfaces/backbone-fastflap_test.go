// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"net"
	"strconv"
	"testing"
	"time"
)

// fastFlapNowFn returns a clock frozen at the given unix time, for the
// time-injectable fast-flap methods.
func fastFlapNowFn(unix int64) func() time.Time {
	t := time.Unix(unix, 0)
	return func() time.Time { return t }
}

// newFastFlapBackbone builds a BackboneInterface with the fast-flap registry
// initialised and the given config, without binding a socket. The nowFn is
// installed so recordFlap / isBlocked / blockedCount are deterministic.
func newFastFlapBackbone(block bool, threshold float64, grace int, expiry float64, now func() time.Time) *BackboneInterface {
	bi := NewBaseInterface("test-flap-backbone", ModeFull, TCPBitrateGuess)
	tsi := &TCPServerInterface{BaseInterface: bi}
	b := &BackboneInterface{TCPServerInterface: tsi}
	b.ConfigureFastFlapping(block, threshold, grace, expiry)
	b.setNowFn(now)
	return b
}

// TestFastFlapRecordAndBlock covers Phase 18 task 1: a BackboneClientInterface
// that disconnects before fast_flap_threshold records a flap for its remote IP,
// and after more than fast_flap_grace flaps the IP is blocked on the next
// incoming_connection (BackboneInterface.py:420-435,820-843, v1.3.9).
func TestFastFlapRecordAndBlock(t *testing.T) {
	t.Parallel()
	// threshold 600s: any sub-minute disconnect counts as a flap.
	// grace 3: block once flaps exceed 3 (i.e. on the 4th flap onward).
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(true, 600, 3, 12*60*60, now)

	const ip = "192.0.2.7"
	spawnedAt := time.Unix(9_999, 0) // connected 1s ago < 600s threshold

	// First 3 flaps (== grace) do NOT block yet: Python rejects only when
	// flaps > grace (BackboneInterface.py:432).
	for i := 1; i <= 3; i++ {
		b.recordFlap(ip, spawnedAt)
		if b.isBlocked(ip) {
			t.Fatalf("after %d flaps (== grace %d), isBlocked=true want false", i, 3)
		}
	}
	// The 4th flap pushes flaps > grace: the IP is now blocked.
	b.recordFlap(ip, spawnedAt)
	if !b.isBlocked(ip) {
		t.Fatalf("after %d flaps (> grace %d), isBlocked=false want true", 4, 3)
	}
}

// TestFastFlapIgnoresLongConnections covers Phase 18 task 1: a disconnect after
// the threshold does NOT record a flap (BackboneInterface.py:828-829).
func TestFastFlapIgnoresLongConnections(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(true, 30, 3, 12*60*60, now)

	const ip = "192.0.2.9"
	spawnedAt := time.Unix(10_000-100, 0) // connected 100s ago > 30s threshold
	b.recordFlap(ip, spawnedAt)
	if b.isBlocked(ip) {
		t.Fatal("long connection recorded a flap; want ignored")
	}
	if b.BlockedIPCount() != 0 {
		t.Fatalf("BlockedIPCount=%d want 0", b.BlockedIPCount())
	}
}

// TestFastFlapDisabledNoBlocking covers Phase 18 task 1: when block_fast_flapping
// is false, no flaps are recorded and no IP is ever blocked
// (BackboneInterface.py:126,424,527).
func TestFastFlapDisabledNoBlocking(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(false, 600, 3, 12*60*60, now)

	const ip = "192.0.2.10"
	spawnedAt := time.Unix(9_999, 0)
	for range 10 {
		b.recordFlap(ip, spawnedAt)
	}
	if b.isBlocked(ip) {
		t.Fatal("block_fast_flapping=false but isBlocked=true")
	}
	if got := b.BlockedIPCount(); got != 0 {
		t.Fatalf("BlockedIPCount=%d want 0 when blocking disabled", got)
	}
}

// TestFastFlapExpiryPurgesStaleEntry covers Phase 18 task 1: an IP whose last
// flap is older than fast_flap_expiry is purged and no longer counted as
// blocked (BackboneInterface.py:539-546).
func TestFastFlapExpiryPurgesStaleEntry(t *testing.T) {
	t.Parallel()
	// expiry 100s; record flaps at t=0, then advance the clock past expiry.
	clock := int64(0)
	now := func() time.Time { return time.Unix(clock, 0) }
	b := newFastFlapBackbone(true, 600, 3, 100, now)

	const ip = "198.51.100.5"
	spawnedAt := time.Unix(0, 0)
	for range 5 {
		b.recordFlap(ip, spawnedAt) // all flaps at t=0
	}
	if !b.isBlocked(ip) {
		t.Fatal("expected blocked before expiry")
	}

	// Advance past the expiry window. isBlocked purges the stale entry.
	clock = 101
	if b.isBlocked(ip) {
		t.Fatal("expected unblocked after expiry")
	}
	if got := b.BlockedIPCount(); got != 0 {
		t.Fatalf("BlockedIPCount after expiry=%d want 0", got)
	}
}

// TestFastFlapIncomingGateRejectsBlockedIP covers Phase 18 task 1: the
// TCPServerInterface incoming gate (set by BackboneInterface) rejects a
// connection from a blocked IP by returning false, mirroring Python's
// incoming_connection returning False (BackboneInterface.py:420-435,397).
func TestFastFlapIncomingGateRejectsBlockedIP(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(true, 600, 3, 12*60*60, now)

	const ip = "203.0.113.4"
	spawnedAt := time.Unix(9_999, 0)
	for range 4 {
		b.recordFlap(ip, spawnedAt)
	}
	gate := b.incomingGate()
	if gate == nil {
		t.Fatal("incoming gate not wired")
	}
	if gate(ip) {
		t.Fatal("gate(blocked IP)=true want false (reject)")
	}
	// A different, non-flapping IP is accepted.
	if !gate("198.51.100.99") {
		t.Fatal("gate(unflapped IP)=false want true (accept)")
	}
}

// TestBackboneFastFlapIntegration covers Phase 18 task 1: driving rapid
// connect/disconnect from one loopback IP exceeds the flap grace and the
// BackboneInterface subsequently rejects further incoming connections from
// that IP, and the blocklist contains exactly that IP (the golden value
// Python's blocked_ip_list would report: list(fast_flapping.keys()))
// (BackboneInterface.py:420-435,820-843,529-533, v1.3.9/1.4.0).
func TestBackboneFastFlapIntegration(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)

	server := mustTestNewBackboneInterface(t, "flap-server", "127.0.0.1", port, func([]byte, Interface) {})
	bb := server.(*BackboneInterface)
	// threshold 600s (any rapid disconnect flaps); grace 3 (block after >3
	// flaps); expiry long so nothing expires mid-test.
	bb.ConfigureFastFlapping(true, 600, 3, 12*60*60)
	defer func() {
		if err := server.Detach(); err != nil {
			t.Fatalf("server detach failed: %v", err)
		}
	}()

	const flapsToBlock = 4 // grace 3 -> 4th flap blocks
	// Drive rapid connect/disconnect from 127.0.0.1. Each short connection
	// records one flap when the server's readLoop observes the EOF. Wait for
	// each flap to land so the count is deterministic.
	for i := range flapsToBlock {
		conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			t.Fatalf("dial %d failed: %v", i, err)
		}
		if err := conn.Close(); err != nil {
			t.Fatalf("close %d failed: %v", i, err)
		}
		waitForFlaps(t, bb, "127.0.0.1", i+1)
	}

	// The IP is now blocked: a fresh dial is accepted by the kernel but the
	// server closes it immediately (the gate rejects before spawning), so the
	// connection either fails or is shut down at or before a byte round-trip.
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		// Rejection can surface as a connect failure if the server closed the
		// socket before the handshake completed; that still proves the block.
		return
	}
	defer func() { _ = conn.Close() }()
	// If the dial succeeded, the server must have closed the connection right
	// away (reject path). A read should return EOF quickly.
	if n, err := conn.Read(make([]byte, 32)); err == nil && n > 0 {
		t.Fatalf("blocked IP delivered data (n=%d); expected immediate reject/close", n)
	}

	// Golden blocklist comparison vs Python's blocked_ip_list: the registry
	// contains exactly the flapping IP.
	list := bb.BlockedIPList()
	if len(list) != 1 || list[0] != "127.0.0.1" {
		t.Fatalf("blocklist=%v want [127.0.0.1]", list)
	}
}

// waitForFlaps polls the fast-flap registry until the IP has recorded at least
// the target flap count, failing the test after a timeout if it never lands.
func waitForFlaps(t *testing.T, b *BackboneInterface, ip string, target int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.fastFlappingMu.Lock()
		e := b.fastFlapping[ip]
		flaps := 0
		if e != nil {
			flaps = e.flaps
		}
		b.fastFlappingMu.Unlock()
		if flaps >= target {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	b.fastFlappingMu.Lock()
	got := 0
	if e := b.fastFlapping[ip]; e != nil {
		got = e.flaps
	}
	b.fastFlappingMu.Unlock()
	t.Fatalf("timed out waiting for %d flaps on %s; got %d", target, ip, got)
}
