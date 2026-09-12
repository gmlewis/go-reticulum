// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// assertNoTempFiles fails the test if storagePath contains any
// "known_destinations.tmp.*" leftover, asserting the atomic save cleaned up
// its temp file on both the success and error paths (Python
// Identity.save_known_destinations, RNS/Identity.py:198-208).
func assertNoTempFiles(t *testing.T, storagePath string) {
	t.Helper()
	entries, err := os.ReadDir(storagePath)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", storagePath, err)
	}
	var leftovers []string
	for _, e := range entries {
		name := e.Name()
		if len(name) > len("known_destinations.tmp.") &&
			name[:len("known_destinations.tmp.")] == "known_destinations.tmp." {
			leftovers = append(leftovers, name)
		}
	}
	if len(leftovers) > 0 {
		sort.Strings(leftovers)
		t.Fatalf("found %d leftover temp file(s): %v", len(leftovers), leftovers)
	}
}

// TestSaveKnownDestinationsIsAtomicTempRename verifies that the
// known-destinations table is written to a temp file and atomically renamed
// into place (Python os.replace, RNS/Identity.py:199-200), so the final
// known_destinations file round-trips and no temp file is left behind.
func TestSaveKnownDestinationsIsAtomicTempRename(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-save-atomic-")

	pubKey := mustTestNewIdentity(t, true).GetPublicKey()
	destHash := []byte("atomic-save-desthash!") // 20 bytes -> truncate to 16 below

	ts.Remember([]byte("pkt-atomic-1"), destHash, pubKey, []byte("app-atomic-1"))

	ts.SaveKnownDestinations(storagePath)

	// The final file must exist at the canonical path, not a temp path.
	if _, err := os.Stat(filepath.Join(storagePath, "known_destinations")); err != nil {
		t.Fatalf("known_destinations not written at canonical path: %v", err)
	}
	assertNoTempFiles(t, storagePath)

	// The written file must round-trip: a fresh transport loading it sees the
	// remembered entry with its 5th use-timestamp element intact.
	ts2 := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts2.LoadKnownDestinations(storagePath)
	ts2.mu.Lock()
	entry, ok := ts2.knownDestinations[string(destHash)]
	ts2.mu.Unlock()
	if !ok {
		t.Fatal("round-tripped known_destinations is missing the remembered entry")
	}
	if got, want := len(entry), 5; got != want {
		t.Fatalf("round-tripped entry len = %d, want %d", got, want)
	}
	if got, ok := entry[2].([]byte); !ok || len(got) != len(pubKey) {
		t.Fatalf("round-tripped entry[2] = %#v, want the public key", entry[2])
	}
	got, ok := numericValue(entry[4])
	if !ok || got != 0 {
		t.Fatalf("round-tripped entry[4] = %#v, want numeric 0 (never used)", entry[4])
	}
}

// TestSaveKnownDestinationsPreservesExistingFileOnWriteError verifies that
// when the temp-file write fails, the existing known_destinations file
// is left intact (the atomic save never touches the canonical path until the
// rename) and the temp file is cleaned up (Python os.unlink on error,
// RNS/Identity.py:204-206).
func TestSaveKnownDestinationsPreservesExistingFileOnWriteError(t *testing.T) {
	t.Parallel()
	storagePath := testutils.TempDir(t, "rns-kd-save-err-")

	// Pre-seed a valid known_destinations file on disk with a known entry.
	writeGoldenKnownDestinations(t, storagePath, goldenKnownDestinations5Hex)
	originalBytes, err := os.ReadFile(filepath.Join(storagePath, "known_destinations"))
	if err != nil {
		t.Fatalf("read seeded file: %v", err)
	}

	// Make the storage directory non-writable so the temp file cannot be
	// created. The canonical known_destinations file is already on disk and
	// must survive the failed save untouched.
	if err := os.Chmod(storagePath, 0o500); err != nil {
		t.Fatalf("chmod storage dir read-only: %v", err)
	}
	t.Cleanup(func() {
		// Restore writability so the temp dir can be cleaned up.
		_ = os.Chmod(storagePath, 0o700)
	})

	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	// A distinct in-memory entry that the failed save must NOT persist.
	newDestHash := []byte("write-err-desthash!!!")
	ts.Remember([]byte("pkt-err"), newDestHash, mustTestNewIdentity(t, true).GetPublicKey(), []byte("app-err"))

	ts.SaveKnownDestinations(storagePath)

	// The existing file must be byte-for-byte intact.
	gotBytes, err := os.ReadFile(filepath.Join(storagePath, "known_destinations"))
	if err != nil {
		t.Fatalf("read existing file after failed save: %v", err)
	}
	if hex.EncodeToString(gotBytes) != hex.EncodeToString(originalBytes) {
		t.Fatalf("existing known_destinations was modified by a failed save:\n want %x\n got  %x",
			originalBytes, gotBytes)
	}

	// Restore writability so assertNoTempFiles can read the dir.
	if err := os.Chmod(storagePath, 0o700); err != nil {
		t.Fatalf("chmod storage dir writable: %v", err)
	}
	assertNoTempFiles(t, storagePath)

	// A fresh transport loading the file must see only the original golden
	// entry, NOT the new one from the failed save.
	ts2 := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts2.LoadKnownDestinations(storagePath)
	ts2.mu.Lock()
	_, hasNew := ts2.knownDestinations[string(newDestHash)]
	_, hasOriginal := ts2.knownDestinations[string(goldenKnownDestDestHash)]
	ts2.mu.Unlock()
	if hasNew {
		t.Fatal("the failed save leaked its new entry into the on-disk file")
	}
	if !hasOriginal {
		t.Fatal("the original golden entry was lost from the on-disk file")
	}
}

// TestSaveKnownDestinationsSkipsSharedInstanceClient verifies that a client of a
// co-located shared Reticulum instance never writes the known-destinations
// table. It shares the storage path with the instance it is connected to, so a
// write would clobber the shared instance's table with the client's
// forwarded-announce-only view. Python returns early from
// save_known_destinations itself (RNS/Identity.py:179), which covers every
// caller — including the stale-ratchet clean-up and the announce-driven saves
// that bypass PersistData.
func TestSaveKnownDestinationsSkipsSharedInstanceClient(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-shared-")
	ts.SetConnectedToSharedInstance(true)

	ts.Remember([]byte("pkt-shared-1"), []byte("shared-instance-dest"),
		mustTestNewIdentity(t, true).GetPublicKey(), []byte("app-shared-1"))
	ts.SaveKnownDestinations(storagePath)

	if _, err := os.Stat(filepath.Join(storagePath, "known_destinations")); !os.IsNotExist(err) {
		t.Fatalf("shared-instance client wrote known_destinations (stat err = %v)", err)
	}
	assertNoTempFiles(t, storagePath)

	// The same transport, once it is no longer a shared-instance client, must
	// persist normally — the gate is a client check, not a broken save.
	ts.SetConnectedToSharedInstance(false)
	ts.SaveKnownDestinations(storagePath)
	if _, err := os.Stat(filepath.Join(storagePath, "known_destinations")); err != nil {
		t.Fatalf("standalone transport did not write known_destinations: %v", err)
	}
	assertNoTempFiles(t, storagePath)
}

// TestKnownDestinationsTempNameUniqueAcrossInstances verifies that the temp file
// an atomic known-destinations save writes through is unique across independent
// RNS instances sharing one storage path, i.e. across processes. A per-process
// counter is not enough: two processes hand out the same temp path, the earlier
// rename wins, and the loser's os.Rename fails with "no such file or
// directory". Python avoids this with a wall-clock timestamp
// (known_destinations.tmp.{time.time()}, RNS/Identity.py:193); os.CreateTemp
// gives the same guarantee via O_EXCL.
func TestKnownDestinationsTempNameUniqueAcrossInstances(t *testing.T) {
	t.Parallel()
	storagePath := testutils.TempDir(t, "rns-kd-tmpuniq-")
	path := filepath.Join(storagePath, "known_destinations")
	const prefix = "known_destinations.tmp."

	first, err := createKnownDestinationsTemp(path)
	if err != nil {
		t.Fatalf("createKnownDestinationsTemp: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := createKnownDestinationsTemp(path)
	if err != nil {
		t.Fatalf("createKnownDestinationsTemp (second instance): %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	if first.Name() == second.Name() {
		t.Fatalf("two instances got the same temp path %q", first.Name())
	}
	// Both must exist at the same time: that is what lets concurrent saves in
	// different processes each rename their own file safely.
	for _, f := range []*os.File{first, second} {
		if _, err := os.Stat(f.Name()); err != nil {
			t.Errorf("temp file %q does not exist: %v", f.Name(), err)
		}
		base := filepath.Base(f.Name())
		if len(base) <= len(prefix) || base[:len(prefix)] != prefix {
			t.Errorf("temp file %q lost the %q prefix that cleanup and leftover checks rely on",
				base, prefix)
		}
	}
}
