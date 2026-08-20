// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestRetainDestinationDataSetsRetainedFlag verifies that
// RetainDestinationData sets the known-destinations use-timestamp (element 4)
// to -1 (retained) for a known destination and returns true; an unknown
// destination returns false (Python Identity._retain_destination_data,
// RNS/Identity.py:252-258).
func TestRetainDestinationDataSetsRetainedFlag(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	destHash := []byte("retain-dest-hash!!!!")
	ts.Remember([]byte("pkt"), destHash, mustTestNewIdentity(t, true).GetPublicKey(), []byte("app"))

	if !ts.RetainDestinationData(destHash) {
		t.Fatal("RetainDestinationData returned false for a known destination")
	}
	ts.mu.Lock()
	entry := ts.knownDestinations[string(destHash)]
	ts.mu.Unlock()
	got, ok := numericValue(entry[4])
	if !ok || got != -1 {
		t.Fatalf("entry[4] = %#v, want -1 (retained)", entry[4])
	}

	// An unknown destination is not retained and returns false.
	unknownHash := []byte("not-a-known-dest!!!!")
	if ts.RetainDestinationData(unknownHash) {
		t.Fatal("RetainDestinationData returned true for an unknown destination")
	}
}

// TestUnretainDestinationDataSetsUseTimestamp verifies that
// UnretainDestinationData sets the use-timestamp to the current time (a
// positive Unix timestamp) for a known destination, clearing the retained
// flag (Python Identity._unretain_destination_data, RNS/Identity.py:261-267).
func TestUnretainDestinationDataSetsUseTimestamp(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	destHash := []byte("unretain-dest-hash!!")
	ts.Remember([]byte("pkt"), destHash, mustTestNewIdentity(t, true).GetPublicKey(), []byte("app"))
	ts.RetainDestinationData(destHash) // set element 4 to -1

	if !ts.UnretainDestinationData(destHash) {
		t.Fatal("UnretainDestinationData returned false for a known destination")
	}
	ts.mu.Lock()
	entry := ts.knownDestinations[string(destHash)]
	ts.mu.Unlock()
	got, ok := numericValue(entry[4])
	// The retained flag (-1) must be cleared to a positive use timestamp. Use
	// a >0 check rather than comparing against a captured "before", since both
	// the method and a captured before compute float64(UnixNano)/1e9, which
	// rounds to ~256ns buckets at the 1.7e18 magnitude and can compare equal.
	if !ok || got <= 0 {
		t.Fatalf("entry[4] = %#v, want a positive use timestamp (cleared from -1)", entry[4])
	}
}

// TestUsedDestinationDataUpdatesUseTimestamp verifies that
// UsedDestinationData sets the use-timestamp to now for a known, non-retained
// destination, but leaves a retained destination (element 4 == -1) untouched
// (Python Identity._used_destination_data, RNS/Identity.py:242-250 — the
// `not entry[4] < 0` guard).
func TestUsedDestinationDataUpdatesUseTimestamp(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	destHash := []byte("used-dest-hash!!!!!!")
	ts.Remember([]byte("pkt"), destHash, mustTestNewIdentity(t, true).GetPublicKey(), []byte("app"))

	if !ts.UsedDestinationData(destHash) {
		t.Fatal("UsedDestinationData returned false for a known, non-retained destination")
	}
	ts.mu.Lock()
	entry := ts.knownDestinations[string(destHash)]
	ts.mu.Unlock()
	got, ok := numericValue(entry[4])
	// The never-used flag (0) must be replaced with a positive use timestamp.
	// See TestUnretainDestinationDataSetsUseTimestamp for the float64-bucket
	// rationale behind the >0 (not >before) check.
	if !ok || got <= 0 {
		t.Fatalf("entry[4] = %#v, want a positive use timestamp (cleared from 0)", entry[4])
	}

	// After retaining, UsedDestinationData must NOT overwrite the -1 flag.
	ts.RetainDestinationData(destHash)
	if ts.UsedDestinationData(destHash) {
		t.Fatal("UsedDestinationData returned true for a retained destination")
	}
	ts.mu.Lock()
	entry = ts.knownDestinations[string(destHash)]
	ts.mu.Unlock()
	got, ok = numericValue(entry[4])
	if !ok || got != -1 {
		t.Fatalf("entry[4] = %#v, want -1 (retained, untouched by UsedDestinationData)", entry[4])
	}
}

// TestRetainIdentityRetainsMatchingDestinations verifies that
// RetainIdentity retains every known destination whose public key hashes
// (truncated) to the given identity hash, and returns true when at least one
// was retained (Python Identity._retain_identity, RNS/Identity.py:270-283).
func TestRetainIdentityRetainsMatchingDestinations(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))

	id := mustTestNewIdentity(t, true)
	pubKey := id.GetPublicKey()
	identityHash := TruncatedHash(pubKey)

	matchingHash := []byte("match-identity-dest!")
	ts.Remember([]byte("pkt-m"), matchingHash, pubKey, []byte("app-m"))

	// A second, non-matching destination must be left unretained.
	otherPub := mustTestNewIdentity(t, true).GetPublicKey()
	otherHash := []byte("other-identity-dest")
	ts.Remember([]byte("pkt-o"), otherHash, otherPub, []byte("app-o"))

	if !ts.RetainIdentity(identityHash) {
		t.Fatal("RetainIdentity returned false despite a matching destination")
	}
	ts.mu.Lock()
	matchEntry := ts.knownDestinations[string(matchingHash)]
	otherEntry := ts.knownDestinations[string(otherHash)]
	ts.mu.Unlock()
	if got, ok := numericValue(matchEntry[4]); !ok || got != -1 {
		t.Fatalf("matching entry[4] = %#v, want -1 (retained)", matchEntry[4])
	}
	if got, ok := numericValue(otherEntry[4]); !ok || got != 0 {
		t.Fatalf("non-matching entry[4] = %#v, want 0 (untouched)", otherEntry[4])
	}

	// Retaining an identity with no matching destinations returns false.
	unknownIdentity := []byte("no-such-identity-hash")
	if ts.RetainIdentity(unknownIdentity) {
		t.Fatal("RetainIdentity returned true for an identity with no matching destinations")
	}
}

// TestRetainAndCleanCycle verifies that a retained, never-used,
// pathless destination survives CleanKnownDestinations, but once unretained it
// is dropped by the next clean — demonstrating the retain + clean lifecycle.
func TestRetainAndCleanCycle(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-retain-clean-")
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	destHash := []byte("cycle-dest-hash!!!!!!")
	putKnownEntry(t, ts, destHash, float64(time.Now().UnixNano())/1e9-3600, 0, "cycle")
	ts.RetainDestinationData(destHash)

	ts.CleanKnownDestinations()
	ts.mu.Lock()
	_, present := ts.knownDestinations[string(destHash)]
	ts.mu.Unlock()
	if !present {
		t.Fatal("retained destination was dropped by CleanKnownDestinations")
	}

	// Unretain (sets a use timestamp to now), then clean. The destination is
	// never-used-no-path again only if we reset element 4 to 0; Unretain sets a
	// recent timestamp, so it is "recently used" and must still be KEPT within
	// DestinationTimeout*1.25. To force a drop, set element 4 back to 0
	// (never used) after unretaining.
	ts.mu.Lock()
	ts.knownDestinations[string(destHash)][4] = int64(0)
	ts.mu.Unlock()

	ts.CleanKnownDestinations()
	ts.mu.Lock()
	_, present = ts.knownDestinations[string(destHash)]
	ts.mu.Unlock()
	if present {
		t.Fatal("never-used pathless destination was not dropped after unretain + clean")
	}
}

// TestIdentityPubToFileWritesOnlyPublicKey verifies that PubToFile
// writes ONLY the public key to the file (not the private key), matching
// Python Identity.pub_to_file (RNS/Identity.py:671-683).
func TestIdentityPubToFileWritesOnlyPublicKey(t *testing.T) {
	t.Parallel()
	id := mustTestNewIdentity(t, true)
	pubKey := id.GetPublicKey()
	if len(pubKey) == 0 {
		t.Fatal("identity has no public key")
	}

	path := filepath.Join(testutils.TempDir(t, "rns-pubtofile-"), "identity.pub")
	if err := id.PubToFile(path); err != nil {
		t.Fatalf("PubToFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pub file: %v", err)
	}
	if len(got) != len(pubKey) {
		t.Fatalf("pub file length = %d, want %d (public key only)", len(got), len(pubKey))
	}
	if string(got) != string(pubKey) {
		t.Fatalf("pub file content = %x, want %x", got, pubKey)
	}
	// The private key must NOT be present in the file. The Go public and
	// private keys are both 64 bytes, so the check is by content, not length.
	prvKey := id.GetPrivateKey()
	if len(prvKey) > 0 && string(got) == string(prvKey) {
		t.Fatal("pub file content equals the private key — private key leaked")
	}
}
