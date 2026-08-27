// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// The fleet symptom this locks down: on a node whose link table routes an
// LRPROOF out interface A while the proof first arrives via interface B
// (normal for multi-plane hosts — LAN + TCP hub + public relays), the
// first-seen copy was remembered in packetHashes and every later copy —
// including the correct-path one — hit "dropping duplicate packet". The
// initiator then sat pending until its watchdog fired ("Link establishment
// timed out"). Python never remembers PROOF+LRPROOF hashes at all
// (Transport.py:1526-1545 remember_packet_hash=False) and removes a rejected
// proof's hash from the list so a corrected copy can arrive
// (Transport.py:2181-2189).

func TestLRProofExemptFromRememberOnFirstSight(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(testSilentLogger())

	proofHash := mustHexDecode(t, "ddd07339657960fb3f67b22a30e481c6bf69480b4abcf9535f5f56e405085eaa")

	ts.mu.Lock()
	ts.ensureStateLocked()
	beforeCur := len(ts.packetHashes)
	beforePrev := len(ts.packetHashesPrev)
	if ts.packetHashSeenLocked(proofHash) {
		ts.mu.Unlock()
		t.Fatal("capture precondition: test hash already present")
	}

	// The Inbound path for isLrproof consults WITHOUT remembering: the map
	// sizes must not grow, unlike seenOrRememberPacketHashLocked which would
	// have inserted the first-seen copy.
	_ = ts.packetHashSeenLocked(proofHash)
	afterCur := len(ts.packetHashes)
	ts.mu.Unlock()

	if afterCur != beforeCur {
		t.Fatalf("packetHashSeenLocked grew current generation: %d -> %d", beforeCur, afterCur)
	}
	if beforePrev != 0 {
		t.Fatalf("unexpected pre-existing previous-generation entries: %d", beforePrev)
	}
}

func TestForgetPacketHashClearsBothGenerations(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(testSilentLogger())

	hash := mustHexDecode(t, "8f1b7b97a29c0550d3f610743e223eb342e5c33be21221383e85fa8d1d8bc674")
	now := timeNowStub()

	// Seed both generations (a rotation may have happened before the reject).
	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.packetHashes[string(hash)] = now.Add(-time.Minute)
	prev := "prev-" + string(hash)
	_ = prev // previous generation is a separate map keyed identically
	ts.packetHashesPrev[string(hash)] = now.Add(-2 * time.Minute)
	ts.mu.Unlock()

	ts.ForgetPacketHash(hash)

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if _, ok := ts.packetHashes[string(hash)]; ok {
		t.Fatal("current-generation entry not removed")
	}
	if _, ok := ts.packetHashesPrev[string(hash)]; ok {
		t.Fatal("previous-generation entry not removed")
	}
}

// timeNowStub returns a fixed reference instant for seeding entries.
func timeNowStub() time.Time { return time.Unix(1_800_000_000, 0) }
