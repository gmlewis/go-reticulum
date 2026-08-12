// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"os"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// saveTwiceInodes performs save twice and returns the os.FileInfo for the
// saved file after each save. A write that overwrites in place (direct
// os.WriteFile) keeps the same inode; an atomic tmp-file + rename replaces
// the file with a new inode each time.
func saveTwiceInodes(t *testing.T, save func() error, path string) (os.FileInfo, os.FileInfo) {
	t.Helper()
	if err := save(); err != nil {
		t.Fatalf("first save: %v", err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after first save: %v", err)
	}
	if err := save(); err != nil {
		t.Fatalf("second save: %v", err)
	}
	second, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after second save: %v", err)
	}
	return first, second
}

// assertAtomicRename verifies that two successive saves replace the file via
// rename (different inode), proving the save is atomic rather than an
// in-place overwrite that exposes a partial file to concurrent readers.
func assertAtomicRename(t *testing.T, save func() error, path string) {
	t.Helper()
	first, second := saveTwiceInodes(t, save, path)
	if os.SameFile(first, second) {
		t.Fatalf("save at %q overwrote in place (same inode); expected atomic rename replacing the inode", path)
	}
}

// TestSaveOutboundStampCostsWritesAtomicallyByRename covers Phase 16 task 3:
// SaveOutboundStampCosts replaces its state file via tmp-file + rename, so the
// file's inode changes on each save (an in-place os.WriteFile would keep the
// same inode), mirroring Python's temp_path + os.replace (LXMRouter.py:1411-
// 1414, v1.1.0).
func TestSaveOutboundStampCostsWritesAtomicallyByRename(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.mu.Lock()
	router.outboundStampCosts = map[string]outboundStampCostEntry{
		"\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10": {
			updatedAt: router.now(),
			stampCost: 4,
		},
	}
	router.mu.Unlock()

	assertAtomicRename(t, router.SaveOutboundStampCosts, router.outboundStampCostsPath())
}

// TestSaveNodeStatsWritesAtomicallyByRename covers Phase 16 task 3 for
// SaveNodeStats: the node-stats file is replaced via rename, not overwritten.
func TestSaveNodeStatsWritesAtomicallyByRename(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.mu.Lock()
	router.clientPropagationMessagesReceived = 7
	router.mu.Unlock()

	assertAtomicRename(t, router.SaveNodeStats, router.nodeStatsPath())
}

// TestSaveAvailableTicketsWritesAtomicallyByRename covers Phase 16 task 3 for
// SaveAvailableTickets.
func TestSaveAvailableTicketsWritesAtomicallyByRename(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	assertAtomicRename(t, router.SaveAvailableTickets, router.availableTicketsPath())
}

// TestSaveLocalTransientIDCachesWritesAtomicallyByRename covers Phase 16 task 3
// for SaveLocalTransientIDCaches: both the locally-delivered and
// locally-processed caches are replaced via rename.
func TestSaveLocalTransientIDCachesWritesAtomicallyByRename(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.mu.Lock()
	router.locallyDeliveredIDs["\x01\x02\x03\x04"] = router.now()
	router.locallyProcessedIDs["\x05\x06\x07\x08"] = router.now()
	router.mu.Unlock()

	assertAtomicRename(t, router.SaveLocalTransientIDCaches, router.localDeliveriesPath())
	assertAtomicRename(t, router.SaveLocalTransientIDCaches, router.locallyProcessedPath())
}

// TestSavePeersWritesAtomicallyByRename covers Phase 16 task 3 for SavePeers.
func TestSavePeersWritesAtomicallyByRename(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.propagationEnabled = true

	assertAtomicRename(t, router.SavePeers, router.peersPath())
}

// TestSaveMethodsLeaveNoTmpRemnant covers Phase 16 task 3: each router state
// Save* writes atomically and leaves no ".tmp.*" remnant in the storage
// directory, mirroring Python's tmp+rename+cleanup for every persisted state
// file (LXMRouter.py:1231-1414, v1.1.0).
func TestSaveMethodsLeaveNoTmpRemnant(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.propagationEnabled = true

	router.mu.Lock()
	router.clientPropagationMessagesReceived = 1
	router.clientPropagationMessagesServed = 2
	router.outboundStampCosts = map[string]outboundStampCostEntry{
		"\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10": {
			updatedAt: router.now(),
			stampCost: 4,
		},
	}
	router.mu.Unlock()

	for _, save := range []func() error{
		router.SaveNodeStats,
		router.SaveOutboundStampCosts,
		router.SaveAvailableTickets,
		router.SaveLocalTransientIDCaches,
		router.SavePeers,
	} {
		if err := save(); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	entries, err := os.ReadDir(router.storagePath)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("leftover tmp file %q in storage after saves", e.Name())
		}
	}
}
