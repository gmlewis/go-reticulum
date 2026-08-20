// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// TestAnnounceRebroadcastDoesNotMarkDestinationUsed verifies that
// the recall performed during an announce rebroadcast uses the transport-
// internal noUse path, so it does NOT update the known-destination use
// timestamp (Python Identity.recall(..., _no_use=True) in the announce
// rebroadcast path, RNS/Transport.py:611-615).
func TestAnnounceRebroadcastDoesNotMarkDestinationUsed(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	id := mustTestNewIdentity(t, true)
	pubKey := id.GetPublicKey()

	destHash := []byte("rebroadcast-nouse-dh!!")
	ts.Remember([]byte("pkt"), destHash, pubKey, []byte("app"))
	// Remember sets element 4 to 0 (never used). The rebroadcast must leave it
	// at 0 rather than stamping the current time.

	// Queue a rebroadcast due now (mirrors the existing
	// TestProcessAnnounceTableRecallableEntryIsRebroadcast setup).
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

	// The rebroadcast must have occurred (Retries incremented)...
	ts.mu.Lock()
	entry, stillQueued := ts.announceTable[string(destHash)]
	useTS, _ := numericValue(ts.knownDestinations[string(destHash)][4])
	ts.mu.Unlock()
	if !stillQueued {
		t.Fatal("recallable announce entry was completed early; nil-guard misfired")
	}
	if entry.Retries != 1 {
		t.Fatalf("entry retries = %d, want 1 (one rebroadcast attempt)", entry.Retries)
	}
	// ...AND the use timestamp must still be 0 (never used), proving the
	// rebroadcast's recall used the noUse path.
	if useTS != 0 {
		t.Fatalf("use timestamp = %v after rebroadcast, want 0 (noUse must not mark used)", useTS)
	}
}

// TestRecallNoUseDoesNotMarkUsed verifies that the transport-internal
// RecallNoUse helper recalls a known destination's identity without updating
// its use timestamp, while the public Recall DOES mark it used (Python
// Identity.recall _no_use flag, RNS/Identity.py:116-160).
func TestRecallNoUseDoesNotMarkUsed(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	destHash := []byte("recall-nouse-dh!!!!!!!!")
	ts.Remember([]byte("pkt"), destHash, mustTestNewIdentity(t, true).GetPublicKey(), []byte("app"))

	// RecallNoUse must not touch the use timestamp.
	if id := ts.RecallNoUse(destHash); id == nil {
		t.Fatal("RecallNoUse returned nil for a known destination")
	}
	ts.mu.Lock()
	useTS, _ := numericValue(ts.knownDestinations[string(destHash)][4])
	ts.mu.Unlock()
	if useTS != 0 {
		t.Fatalf("RecallNoUse set use timestamp = %v, want 0 (noUse must not mark used)", useTS)
	}

	// The public Recall must mark it used (element 4 becomes a positive
	// timestamp), mirroring Python's default _no_use=False.
	if id := ts.Recall(destHash); id == nil {
		t.Fatal("Recall returned nil for a known destination")
	}
	ts.mu.Lock()
	useTS, _ = numericValue(ts.knownDestinations[string(destHash)][4])
	ts.mu.Unlock()
	if useTS <= 0 {
		t.Fatalf("public Recall use timestamp = %v, want a positive value (marked used)", useTS)
	}
}
