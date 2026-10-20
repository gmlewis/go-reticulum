// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
)

// TestSharedInstanceClientDoesNotDeduplicate pins Python's client-plane rule:
// a client of a shared instance neither filters nor deduplicates inbound
// frames, because the instance does both for it. The same frame delivered
// twice must reach the local destination twice, and no packet hash may be
// remembered (Python's add_packet_hash is a no-op for a client,
// Transport.py:1372-1375; its filter returns True immediately,
// Transport.py:1379-1381).
func TestSharedInstanceClientDoesNotDeduplicate(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.SetConnectedToSharedInstance(true)
	_, raw, got := readyWaitDest(t, ts, "client-dedup", "same-frame")
	iface := &dummyInterface{name: "rx"}

	ts.Inbound(raw, iface)
	ts.Inbound(raw, iface)

	if n := got.Load(); n != 2 {
		t.Fatalf("client processed the repeated frame %v time(s), want 2", n)
	}
	ts.mu.Lock()
	remembered := len(ts.packetHashes) + len(ts.packetHashesPrev)
	ts.mu.Unlock()
	if remembered != 0 {
		t.Errorf("client remembered %v packet hash(es), want none", remembered)
	}
}

// TestStandaloneTransportDeduplicates is the complement: a standalone
// transport still drops a repeated frame and remembers the hash.
func TestStandaloneTransportDeduplicates(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	_, raw, got := readyWaitDest(t, ts, "standalone-dedup", "same-frame")
	iface := &dummyInterface{name: "rx"}

	ts.Inbound(raw, iface)
	ts.Inbound(raw, iface)

	if n := got.Load(); n != 1 {
		t.Fatalf("standalone transport processed the repeated frame %v time(s), want 1", n)
	}
}
