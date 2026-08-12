// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestSaveKnownDestinationsWithRecombineMergesMissingDiskEntries covers Phase
// 13 task 3: SaveKnownDestinationsWithRecombine loads known-destinations
// entries currently on disk that are NOT in the in-memory table and merges
// them in before persisting, so a disk-only entry survives the save
// (historical recombine=True behavior, RNS/Identity.py pre-b408699e). The
// merge is "memory wins": a disk entry already in memory is not overwritten.
func TestSaveKnownDestinationsWithRecombineMergesMissingDiskEntries(t *testing.T) {
	t.Parallel()
	storagePath := testutils.TempDir(t, "rns-kd-recombine-")

	// Seed the disk file with a 5-element golden entry (A) that is NOT in the
	// in-memory table of the saver below.
	writeGoldenKnownDestinations(t, storagePath, goldenKnownDestinations5Hex)

	// A fresh in-memory table with a DIFFERENT entry (B). Without recombine,
	// saving would overwrite the disk file with only B and lose A.
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	destHashB := []byte("recombine-desthash!!")
	pubKeyB := mustTestNewIdentity(t, true).GetPublicKey()
	ts.Remember([]byte("pkt-B"), destHashB, pubKeyB, []byte("app-B"))

	ts.SaveKnownDestinationsWithRecombine(storagePath)

	// A fresh transport loading the result must see BOTH the disk-merged entry
	// A and the in-memory entry B.
	ts2 := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts2.LoadKnownDestinations(storagePath)
	ts2.mu.Lock()
	entryA, hasA := ts2.knownDestinations[string(goldenKnownDestDestHash)]
	entryB, hasB := ts2.knownDestinations[string(destHashB)]
	ts2.mu.Unlock()
	if !hasA {
		t.Fatal("recombine dropped the disk-only golden entry A")
	}
	if !hasB {
		t.Fatal("recombine dropped the in-memory entry B")
	}
	if got, want := len(entryA), 5; got != want {
		t.Fatalf("merged entry A len = %d, want %d", got, want)
	}
	// The merged disk entry A must retain its golden app_data (deadbeef),
	// proving the disk copy was merged verbatim, not overwritten by entry B.
	if got, ok := entryA[3].([]byte); !ok || string(got) != "\xde\xad\xbe\xef" {
		t.Fatalf("merged entry A app_data = %#v, want deadbeef", entryA[3])
	}
	if got, want := len(entryB), 5; got != want {
		t.Fatalf("in-memory entry B len = %d, want %d", got, want)
	}
}

// TestSaveKnownDestinationsWithRecombineMemoryWins covers Phase 13 task 3: when
// a destination hash is present in BOTH the on-disk file and the in-memory
// table, the in-memory entry wins — recombine must NOT overwrite it with the
// disk copy.
func TestSaveKnownDestinationsWithRecombineMemoryWins(t *testing.T) {
	t.Parallel()
	storagePath := testutils.TempDir(t, "rns-kd-recombine-mw-")

	// Disk file holds a golden 5-element entry keyed by goldenKnownDestDestHash.
	writeGoldenKnownDestinations(t, storagePath, goldenKnownDestinations5Hex)

	// The in-memory table has the SAME hash but a distinct app_data (app-mem),
	// so we can detect which copy survived the merge.
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	pubKey := mustTestNewIdentity(t, true).GetPublicKey()
	ts.Remember([]byte("pkt-mem"), goldenKnownDestDestHash, pubKey, []byte("app-mem"))

	ts.SaveKnownDestinationsWithRecombine(storagePath)

	ts2 := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts2.LoadKnownDestinations(storagePath)
	ts2.mu.Lock()
	entry, ok := ts2.knownDestinations[string(goldenKnownDestDestHash)]
	ts2.mu.Unlock()
	if !ok {
		t.Fatal("the shared-hash entry was lost")
	}
	if got, ok := entry[3].([]byte); !ok || string(got) != "app-mem" {
		t.Fatalf("recombine overwrote the in-memory entry with the disk copy: entry[3] = %#v, want app-mem", entry[3])
	}
}
