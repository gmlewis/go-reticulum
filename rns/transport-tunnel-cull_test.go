// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// randomBlobWithTimebase builds a 10-byte announce replay blob whose
// timebase field (big-endian uint40 at bytes [5:10],
// Transport.timebase_from_random_blob, Transport.py:3272-3274) encodes the
// given value, so timebaseFromRandomBlobs returns exactly tb.
func randomBlobWithTimebase(tb uint64) []byte {
	b := make([]byte, 10)
	for i := range 5 {
		b[9-i] = byte(tb >> (8 * i))
	}
	return b
}

// TestCullTunnelsDropsPathSupersededByNewerActivePath verifies that
// the tunnel-path cull drops a tunnel path when the active path table entry
// for the same destination has a more recent announce timebase (Python
// Transport.py:860-867: current_path_timebase > tunnel_announce_timebase).
func TestCullTunnelsDropsPathSupersededByNewerActivePath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	now := time.Now()

	const destKey = "destination-A"
	tunnelPath := &PathEntry{
		Timestamp:   now,
		RandomBlobs: [][]byte{randomBlobWithTimebase(100)},
	}
	activePath := &PathEntry{
		Timestamp:   now,
		RandomBlobs: [][]byte{randomBlobWithTimebase(200)},
	}

	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.tunnels = map[string]*Tunnel{
		"tunnel1": {ID: []byte("tunnel1"), Paths: map[string]*PathEntry{destKey: tunnelPath}, Expires: now.Add(time.Hour)},
	}
	ts.pathTable[destKey] = activePath
	ts.mu.Unlock()

	ts.cullTunnels(now)

	ts.mu.Lock()
	tunnel := ts.tunnels["tunnel1"]
	ts.mu.Unlock()
	if tunnel == nil {
		t.Fatal("tunnel was removed entirely; only the superseded path should be dropped")
	}
	if _, stillThere := tunnel.Paths[destKey]; stillThere {
		t.Fatal("superseded tunnel path was not dropped by cullTunnels")
	}
}

// TestCullTunnelsKeepsPathWhenActivePathIsOlder verifies that when
// the active path table entry has an OLDER announce timebase than the tunnel
// path, the tunnel path is retained (the comparison is strict greater-than).
func TestCullTunnelsKeepsPathWhenActivePathIsOlder(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	now := time.Now()

	const destKey = "destination-B"
	tunnelPath := &PathEntry{
		Timestamp:   now,
		RandomBlobs: [][]byte{randomBlobWithTimebase(300)},
	}
	activePath := &PathEntry{
		Timestamp:   now,
		RandomBlobs: [][]byte{randomBlobWithTimebase(200)},
	}

	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.tunnels = map[string]*Tunnel{
		"tunnel1": {ID: []byte("tunnel1"), Paths: map[string]*PathEntry{destKey: tunnelPath}, Expires: now.Add(time.Hour)},
	}
	ts.pathTable[destKey] = activePath
	ts.mu.Unlock()

	ts.cullTunnels(now)

	ts.mu.Lock()
	tunnel := ts.tunnels["tunnel1"]
	ts.mu.Unlock()
	if tunnel == nil {
		t.Fatal("tunnel was unexpectedly removed")
	}
	if _, ok := tunnel.Paths[destKey]; !ok {
		t.Fatal("tunnel path was dropped despite having a newer timebase than the active path")
	}
}

// TestCullTunnelsDropsPathPastPathTimeout verifies that a tunnel
// path whose timestamp is older than TUNNEL_PATH_TIMEOUT is dropped even with
// no competing active path (Python Transport.py:851-852).
func TestCullTunnelsDropsPathPastPathTimeout(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	now := time.Now()

	const destKey = "destination-C"
	tunnelPath := &PathEntry{
		Timestamp:   now.Add(-TunnelPathTimeout - time.Minute),
		RandomBlobs: [][]byte{randomBlobWithTimebase(100)},
	}

	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.tunnels = map[string]*Tunnel{
		"tunnel1": {ID: []byte("tunnel1"), Paths: map[string]*PathEntry{destKey: tunnelPath}, Expires: now.Add(time.Hour)},
	}
	ts.mu.Unlock()

	ts.cullTunnels(now)

	ts.mu.Lock()
	tunnel := ts.tunnels["tunnel1"]
	ts.mu.Unlock()
	if tunnel == nil {
		t.Fatal("tunnel was removed entirely; only the timed-out path should drop")
	}
	if _, stillThere := tunnel.Paths[destKey]; stillThere {
		t.Fatal("timed-out tunnel path was not dropped by cullTunnels")
	}
}

// TestCullTunnelsKeepsPathWithNoActivePath verifies that a tunnel
// path whose destination has no active path table entry is retained (the
// "guard against missing tunnel IDs" / missing active-path branch).
func TestCullTunnelsKeepsPathWithNoActivePath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	now := time.Now()

	const destKey = "destination-D"
	tunnelPath := &PathEntry{
		Timestamp:   now,
		RandomBlobs: [][]byte{randomBlobWithTimebase(100)},
	}

	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.tunnels = map[string]*Tunnel{
		"tunnel1": {ID: []byte("tunnel1"), Paths: map[string]*PathEntry{destKey: tunnelPath}, Expires: now.Add(time.Hour)},
	}
	ts.mu.Unlock()

	ts.cullTunnels(now)

	ts.mu.Lock()
	tunnel := ts.tunnels["tunnel1"]
	ts.mu.Unlock()
	if tunnel == nil {
		t.Fatal("tunnel was unexpectedly removed")
	}
	if _, ok := tunnel.Paths[destKey]; !ok {
		t.Fatal("tunnel path with no competing active path was dropped")
	}
}

// TestCullTunnelsRemovesExpiredTunnel verifies that a tunnel past
// its TUNNEL_TIMEOUT expiry is removed entirely (Python Transport.py:837-838).
func TestCullTunnelsRemovesExpiredTunnel(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	now := time.Now()

	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.tunnels = map[string]*Tunnel{
		"tunnel1": {ID: []byte("tunnel1"), Paths: map[string]*PathEntry{}, Expires: now.Add(-time.Minute)},
	}
	ts.mu.Unlock()

	ts.cullTunnels(now)

	ts.mu.Lock()
	_, stillThere := ts.tunnels["tunnel1"]
	ts.mu.Unlock()
	if stillThere {
		t.Fatal("expired tunnel was not removed by cullTunnels")
	}
}

// TestHandleTunnelUsesTunnelTimeoutExpiry verifies that HandleTunnel
// sets the tunnel expiry to now + TUNNEL_TIMEOUT (8h), not the week-long
// DESTINATION_TIMEOUT (Python Transport.py:2422).
func TestHandleTunnelUsesTunnelTimeoutExpiry(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	iface := newBootstrapConstructorTestInterface("tun", "TCPServerInterface")

	if err := ts.HandleTunnel([]byte("tunnel-id-1"), iface); err != nil {
		t.Fatalf("HandleTunnel returned error: %v", err)
	}

	before := time.Now()
	ts.mu.Lock()
	tunnel := ts.tunnels["tunnel-id-1"]
	ts.mu.Unlock()
	if tunnel == nil {
		t.Fatal("HandleTunnel did not create the tunnel entry")
	}
	// Expiry must be ~now + TunnelTimeout, nowhere near the week-long
	// DestinationTimeout.
	if got := tunnel.Expires; got.Before(before.Add(TunnelTimeout-time.Second)) || got.After(time.Now().Add(TunnelTimeout+time.Second)) {
		t.Fatalf("tunnel expiry = %v, want ~now+%v (TUNNEL_TIMEOUT)", got, TunnelTimeout)
	}
}

// TestHandleTunnelGuardsAgainstMissingTunnelID verifies that
// HandleTunnel rejects a nil/empty tunnel ID instead of creating a malformed
// entry keyed on the empty string.
func TestHandleTunnelGuardsAgainstMissingTunnelID(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	iface := newBootstrapConstructorTestInterface("tun", "TCPServerInterface")

	if err := ts.HandleTunnel(nil, iface); err == nil {
		t.Fatal("HandleTunnel(nil) returned nil error; want a guard error")
	}
	if err := ts.HandleTunnel([]byte{}, iface); err == nil {
		t.Fatal("HandleTunnel(empty) returned nil error; want a guard error")
	}

	ts.mu.Lock()
	n := len(ts.tunnels)
	ts.mu.Unlock()
	if n != 0 {
		t.Fatalf("tunnels map has %d entries after rejected HandleTunnel calls, want 0", n)
	}
}
