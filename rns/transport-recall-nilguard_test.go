// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// rememberDestinationForTest caches a known destination so recallLocked can
// resolve it, mirroring what ValidateAnnounce's Remember call does for an
// accepted announce (identity.go:284).
func rememberDestinationForTest(t *testing.T, ts *TransportSystem, destHash, pubKey []byte) {
	t.Helper()
	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.knownDestinations[string(destHash)] = []any{
		float64(time.Now().UnixNano()) / 1e9,
		[]byte("packet-hash"),
		pubKey,
		nil,
	}
	ts.mu.Unlock()
}

// TestProcessAnnounceTableRecallNilGuardCompletesEntry covers Phase 12 task 10:
// when the identity for a queued announce can no longer be recalled because the
// known-destination was removed mid-retransmit ("the path was cleaned while
// waiting for announce rebroadcast"), processAnnounceTable completes the
// announce entry instead of proceeding to rebroadcast (Python
// Transport.py:611-615, v1.2.5).
func TestProcessAnnounceTableRecallNilGuardCompletesEntry(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	id := mustTestNewIdentity(t, true)
	pubKey := id.GetPublicKey()

	destHash := []byte("recall-nilguard-dest-hash!!") // 24 bytes; map key only
	rememberDestinationForTest(t, ts, destHash, pubKey)

	// Queue a rebroadcast due now.
	ts.mu.Lock()
	ts.announceTable[string(destHash)] = &AnnounceEntry{
		PacketRaw:         []byte{0x00, 0x01},
		SourceInterface:   &dummyInterface{name: "src"},
		Hops:              1,
		NextRebroadcastAt: time.Now().Add(-time.Minute),
		Retries:           0,
	}
	// Remove the known-destination mid-retransmit: recall will now return nil
	// (the destination is not locally registered either).
	delete(ts.knownDestinations, string(destHash))
	ts.mu.Unlock()

	completed := completesWithin(func() { ts.processAnnounceTable(time.Now()) }, time.Second)
	if !completed {
		t.Fatal("processAnnounceTable did not return promptly; nil-guard may have panicked")
	}

	ts.mu.Lock()
	_, stillQueued := ts.announceTable[string(destHash)]
	ts.mu.Unlock()
	if stillQueued {
		t.Fatal("announce entry was not completed when the recalled identity was nil")
	}
}

// TestProcessAnnounceTableRecallableEntryIsRebroadcast covers Phase 12 task 10:
// when the identity IS recallable, processAnnounceTable does NOT complete the
// entry early — it increments retries and keeps rebroadcasting. This confirms
// the nil-guard only triggers on a nil recall, not on every entry.
func TestProcessAnnounceTableRecallableEntryIsRebroadcast(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	id := mustTestNewIdentity(t, true)
	pubKey := id.GetPublicKey()

	destHash := []byte("recall-present-dest-hash!!!")
	rememberDestinationForTest(t, ts, destHash, pubKey)

	ts.mu.Lock()
	ts.announceTable[string(destHash)] = &AnnounceEntry{
		PacketRaw:         []byte{0x00, 0x01},
		SourceInterface:   &dummyInterface{name: "src"},
		Hops:              1,
		NextRebroadcastAt: time.Now().Add(-time.Minute),
		Retries:           0,
	}
	ts.mu.Unlock()

	ts.processAnnounceTable(time.Now())

	ts.mu.Lock()
	entry, stillQueued := ts.announceTable[string(destHash)]
	ts.mu.Unlock()
	if !stillQueued {
		t.Fatal("recallable announce entry was completed early; nil-guard misfired")
	}
	if entry.Retries != 1 {
		t.Fatalf("recallable entry retries = %d, want 1 (one rebroadcast attempt)", entry.Retries)
	}
}

// TestRecallLockedReturnsNilForUnknownDestination covers Phase 12 task 10:
// recallLocked returns nil for a destination that is neither a known
// destination nor a locally-registered destination, which is the condition the
// nil-guard checks.
func TestRecallLockedReturnsNilForUnknownDestination(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))

	ts.mu.Lock()
	got := ts.recallLocked([]byte("totally-unknown-dest-hash!"), true)
	ts.mu.Unlock()
	if got != nil {
		t.Fatalf("recallLocked(unknown) = %v, want nil", got)
	}
}
