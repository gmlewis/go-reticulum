// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// putKnownEntry installs a known-destinations entry for destHash with the
// given last-announce Unix seconds, use-timestamp (0=never used, -1=retained,
// >0=last-used Unix seconds) and app data, so a test can drive
// CleanKnownDestinations with precise staleness inputs.
func putKnownEntry(t *testing.T, ts *TransportSystem, destHash []byte, lastAnnounce, useTS float64, appData string) {
	t.Helper()
	pubKey := mustTestNewIdentity(t, true).GetPublicKey()
	var useAny any
	switch {
	case useTS == -1:
		useAny = int64(-1)
	case useTS == 0:
		useAny = int64(0)
	default:
		useAny = useTS
	}
	ts.mu.Lock()
	ts.knownDestinations[string(destHash)] = []any{
		lastAnnounce, []byte("pkt-" + appData), pubKey, []byte(appData), useAny,
	}
	ts.mu.Unlock()
}

// addPath installs a minimal path-table entry for destHash so HasPath returns
// true and CleanKnownDestinations keeps it.
func addPath(t *testing.T, ts *TransportSystem, destHash []byte) {
	t.Helper()
	ts.mu.Lock()
	ts.pathTable[string(destHash)] = &PathEntry{}
	ts.mu.Unlock()
}

// TestCleanKnownDestinationsDropsStaleNeverUsedNoPath verifies that a
// never-used (use-timestamp 0), pathless known destination is dropped by
// CleanKnownDestinations (Python Identity.clean_known_destinations,
// RNS/Identity.py:321-325 — unused_for exceeds DESTINATION_TIMEOUT*1.25 for a
// never-used entry since unused_for = now - 0).
func TestCleanKnownDestinationsDropsStaleNeverUsedNoPath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-clean-never-")
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	staleHash := []byte("stale-never-desthash")
	putKnownEntry(t, ts, staleHash, float64(time.Now().UnixNano())/1e9-3600, 0, "stale")

	ts.CleanKnownDestinations()

	ts.mu.Lock()
	_, present := ts.knownDestinations[string(staleHash)]
	ts.mu.Unlock()
	if present {
		t.Fatal("never-used pathless entry was not dropped by CleanKnownDestinations")
	}
}

// TestCleanKnownDestinationsKeepsEntryWithPath verifies that a
// known destination with a current path is kept even when never used.
func TestCleanKnownDestinationsKeepsEntryWithPath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-clean-path-")
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	keptHash := []byte("kept-path-desthash!!")
	putKnownEntry(t, ts, keptHash, float64(time.Now().UnixNano())/1e9-3600, 0, "kept")
	addPath(t, ts, keptHash)

	ts.CleanKnownDestinations()

	ts.mu.Lock()
	_, present := ts.knownDestinations[string(keptHash)]
	ts.mu.Unlock()
	if !present {
		t.Fatal("entry with a current path was dropped by CleanKnownDestinations")
	}
}

// TestCleanKnownDestinationsKeepsRetainedEntry verifies that a
// retained destination (use-timestamp -1) is kept even when pathless.
func TestCleanKnownDestinationsKeepsRetainedEntry(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-clean-ret-")
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	retainedHash := []byte("retained-desthash!!!")
	putKnownEntry(t, ts, retainedHash, float64(time.Now().UnixNano())/1e9-3600, -1, "retained")

	ts.CleanKnownDestinations()

	ts.mu.Lock()
	_, present := ts.knownDestinations[string(retainedHash)]
	ts.mu.Unlock()
	if !present {
		t.Fatal("retained entry was dropped by CleanKnownDestinations")
	}
}

// TestCleanKnownDestinationsDropsUsedEntryPastTimeout verifies that a
// used destination whose last use is older than DestinationTimeout*1.25 and
// has no path is dropped.
func TestCleanKnownDestinationsDropsUsedEntryPastTimeout(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-clean-used-")
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	nowSec := float64(time.Now().UnixNano()) / 1e9
	// Last used 10 days ago — past DestinationTimeout*1.25 (~8.75 days).
	staleUsedHash := []byte("stale-used-desthash!")
	putKnownEntry(t, ts, staleUsedHash, nowSec-10*24*3600, nowSec-10*24*3600, "stale-used")

	ts.CleanKnownDestinations()

	ts.mu.Lock()
	_, present := ts.knownDestinations[string(staleUsedHash)]
	ts.mu.Unlock()
	if present {
		t.Fatal("used entry past DestinationTimeout*1.25 was not dropped")
	}
}

// TestCleanKnownDestinationsKeepsRecentlyUsedEntry verifies that a
// used destination whose last use is within DestinationTimeout*1.25 is kept
// even when pathless.
func TestCleanKnownDestinationsKeepsRecentlyUsedEntry(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-clean-recent-")
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	nowSec := float64(time.Now().UnixNano()) / 1e9
	// Last used 1 day ago — within DestinationTimeout*1.25.
	keptHash := []byte("recent-used-desthash")
	putKnownEntry(t, ts, keptHash, nowSec-24*3600, nowSec-24*3600, "recent")

	ts.CleanKnownDestinations()

	ts.mu.Lock()
	_, present := ts.knownDestinations[string(keptHash)]
	ts.mu.Unlock()
	if !present {
		t.Fatal("recently used entry was dropped by CleanKnownDestinations")
	}
}

// TestCleanKnownDestinationsRemovesRatchetFile verifies that when a
// stale entry is dropped, its on-disk ratchet file (ratchets/<hexhash>) is
// unlinked best-effort (Python RNS/Identity.py:333-340).
func TestCleanKnownDestinationsRemovesRatchetFile(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-clean-ratchet-")
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	staleHash := []byte("ratchet-clean-dh!!!!")
	putKnownEntry(t, ts, staleHash, float64(time.Now().UnixNano())/1e9-3600, 0, "stale")

	ratchetDir := filepath.Join(storagePath, "ratchets")
	if err := os.MkdirAll(ratchetDir, 0o700); err != nil {
		t.Fatalf("MkdirAll ratchets: %v", err)
	}
	ratchetPath := filepath.Join(ratchetDir, fmt.Sprintf("%x", staleHash))
	if err := os.WriteFile(ratchetPath, []byte("ratchet-data"), 0o600); err != nil {
		t.Fatalf("write ratchet file: %v", err)
	}

	ts.CleanKnownDestinations()

	if _, err := os.Stat(ratchetPath); !os.IsNotExist(err) {
		t.Fatalf("stale ratchet file still exists after clean: %v", err)
	}
	ts.mu.Lock()
	_, present := ts.knownDestinations[string(staleHash)]
	ts.mu.Unlock()
	if present {
		t.Fatal("stale entry was not dropped")
	}
}
