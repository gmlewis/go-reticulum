// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// TestAnnounceMoreHopsReplacesOnNewerEmission verifies the missing `else`
// branch of handleAnnounce for packet.Hops > entry.Hops. Python
// (Transport.py:1846-1890) replaces a known path with a longer-hop announce
// when the announce was more recently emitted (new random blob + newer
// emission timestamp), even though it has more hops. The Go port was missing
// this entire branch, so a longer-hop but newer announce was silently dropped.
func TestAnnounceMoreHopsReplacesOnNewerEmission(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	iface := &capturingInterface{name: "rx-newer", gravity: 0}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "more-hops-newer")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// Plausible emission timebases (near local time): the poison-heal
	// plausibility gate only stores blobs whose emission looks like a real
	// unix timestamp, so the newer-emission comparison below needs realistic
	// values rather than synthetic small ones.
	base := uint64(time.Now().Unix())

	// First announce: emission=base, wire hops=1 → packet.Hops=2 after inbound++.
	p1 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, base)
	p1.Hops = 1
	if err := p1.Pack(); err != nil {
		t.Fatalf("Pack p1: %v", err)
	}
	ts.Inbound(append([]byte(nil), p1.Raw...), iface)

	// Verify path installed with hops=2.
	ts.mu.Lock()
	entry, ok := ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("expected path table entry after first announce")
	}
	if entry.Hops != 2 {
		t.Fatalf("first announce: hops = %v, want 2", entry.Hops)
	}

	// Second announce: emission=base+1h (newer), wire hops=3 → packet.Hops=4.
	// This has MORE hops but a NEWER emission. Python replaces; Go pre-fix
	// silently dropped it.
	p2 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, base+3600)
	p2.Hops = 3
	if err := p2.Pack(); err != nil {
		t.Fatalf("Pack p2: %v", err)
	}
	ts.Inbound(append([]byte(nil), p2.Raw...), iface)

	// After the fix, the path should be replaced with the 4-hop path
	// (packet.Hops after inbound++ = 4), because the emission is newer.
	ts.mu.Lock()
	entry, ok = ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("path table entry disappeared after second announce")
	}
	if entry.Hops != 4 {
		t.Errorf("after newer-emit more-hops announce: hops = %v, want 4 (Python replaces with newer emission even if more hops)", entry.Hops)
	}
}

// TestAnnounceMoreHopsReplacesOnExpiredPath verifies the expired-path branch:
// when the existing path has expired and a new (unseen random blob) announce
// arrives with more hops, Python replaces the path (Transport.py:1861-1869).
func TestAnnounceMoreHopsReplacesOnExpiredPath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	iface := &capturingInterface{name: "rx-expired", gravity: 0}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "more-hops-expired")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// Plausible emission timebases (see TestAnnounceMoreHopsReplacesOnNewerEmission).
	base := uint64(time.Now().Unix())

	// First announce: emission=base, wire hops=1 → hops=2.
	p1 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, base)
	p1.Hops = 1
	if err := p1.Pack(); err != nil {
		t.Fatalf("Pack p1: %v", err)
	}
	ts.Inbound(append([]byte(nil), p1.Raw...), iface)

	// Expire the path manually.
	ts.mu.Lock()
	entry, ok := ts.pathTable[string(dest.Hash)]
	if !ok {
		ts.mu.Unlock()
		t.Fatal("expected path table entry")
	}
	entry.Expires = time.Now().Add(-time.Hour) // expired 1 hour ago
	ts.mu.Unlock()

	// Second announce: emission=base+1h (newer, new random blob), wire hops=3 → hops=4.
	p2 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, base+3600)
	p2.Hops = 3
	if err := p2.Pack(); err != nil {
		t.Fatalf("Pack p2: %v", err)
	}
	ts.Inbound(append([]byte(nil), p2.Raw...), iface)

	ts.mu.Lock()
	entry, ok = ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("path table entry disappeared")
	}
	if entry.Hops != 4 {
		t.Errorf("after expired-path more-hops announce: hops = %v, want 4 (Python replaces expired path with new announce)", entry.Hops)
	}
}

// TestAnnounceMoreHopsReplacesOnUnresponsivePath verifies the unresponsive-path
// branch: when the existing path is marked unresponsive and a same-emission
// announce arrives with more hops, Python replaces the path
// (Transport.py:1886-1890).
func TestAnnounceMoreHopsReplacesOnUnresponsivePath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	iface := &capturingInterface{name: "rx-unresponsive", gravity: 0}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "more-hops-unresponsive")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// A plausible shared emission timebase (near local time): the poison-heal
	// plausibility gate only stores blobs whose emission looks like a real
	// unix timestamp. A shared realistic value pins the test to the
	// same-timebase unresponsive branch — with a synthetic small emission the
	// blob is never stored and the announce replaces via the wrong
	// (more-recently-emitted) branch.
	emission := uint64(time.Now().Unix())

	// First announce: emission=emission, wire hops=1 → hops=2.
	p1 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, emission)
	p1.Hops = 1
	if err := p1.Pack(); err != nil {
		t.Fatalf("Pack p1: %v", err)
	}
	ts.Inbound(append([]byte(nil), p1.Raw...), iface)

	// Mark the path as unresponsive.
	ts.MarkPathUnresponsive(dest.Hash)

	// Second announce: SAME emission (same timebase), wire hops=3 → hops=4.
	// Same random blob (same emission → same blob), so newBlob is false.
	// Python replaces because the path is unresponsive (Transport.py:1886-1890).
	p2 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, emission)
	p2.Hops = 3
	if err := p2.Pack(); err != nil {
		t.Fatalf("Pack p2: %v", err)
	}
	ts.Inbound(append([]byte(nil), p2.Raw...), iface)

	ts.mu.Lock()
	entry, ok := ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("path table entry disappeared")
	}
	if entry.Hops != 4 {
		t.Errorf("after unresponsive-path more-hops announce: hops = %v, want 4 (Python replaces unresponsive path)", entry.Hops)
	}
}

// TestAnnounceMoreHopsDroppedWhenNoConditionMet verifies that a longer-hop
// announce is correctly dropped when none of the replacement conditions are met
// (path not expired, same emission, path responsive). This is the "ignore it"
// default behavior (Transport.py:1849-1850, 1890).
func TestAnnounceMoreHopsDroppedWhenNoConditionMet(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	iface := &capturingInterface{name: "rx-drop", gravity: 0}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "more-hops-drop")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// A plausible emission timebase (near local time): the poison-heal
	// plausibility gate only stores blobs whose emission looks like a real
	// unix timestamp, so the same-emission comparison below needs a realistic
	// value rather than a synthetic small one.
	emission := uint64(time.Now().Unix())

	// First announce: emission=emission, wire hops=1 → hops=2.
	p1 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, emission)
	p1.Hops = 1
	if err := p1.Pack(); err != nil {
		t.Fatalf("Pack p1: %v", err)
	}
	ts.Inbound(append([]byte(nil), p1.Raw...), iface)

	// Second announce: SAME emission, wire hops=3 → hops=4.
	// Path not expired, same emission, path responsive → should be dropped.
	p2 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, emission)
	p2.Hops = 3
	if err := p2.Pack(); err != nil {
		t.Fatalf("Pack p2: %v", err)
	}
	ts.Inbound(append([]byte(nil), p2.Raw...), iface)

	ts.mu.Lock()
	entry, ok := ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("path table entry disappeared")
	}
	if entry.Hops != 2 {
		t.Errorf("after same-emit more-hops announce (no condition met): hops = %v, want 2 (should keep shorter path)", entry.Hops)
	}
}
