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

// TestStopVoidsQueues verifies that Stop calls voidQueuesLocked so
// the in-memory transport queues — outstanding receipts, the reverse table,
// and each interface's held-announce deque — are empty after the transport
// stops (Python Transport.void_queues / exit_handler, Transport.py:3517-3525).
func TestStopVoidsQueues(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-void-queues-")
	ts := NewTransportSystem(nil)
	if err := ts.Start(tmpDir); err != nil {
		t.Fatalf("Start: %v", err)
	}

	iface := newBootstrapConstructorTestInterface("voidtest", "TCPServerInterface")
	ts.RegisterInterface(iface)

	// Hold an announce on the interface so the held-announce deque is non-empty
	// before Stop.
	iface.HoldAnnounce([]byte("raw-announce"), iface, 1, []byte("desthash"))
	if got := iface.HeldAnnounces(); got != 1 {
		t.Fatalf("precondition: HeldAnnounces = %v, want 1", got)
	}

	// Seed an outstanding receipt and a reverse-table entry.
	ts.mu.Lock()
	ts.receipts = append(ts.receipts, &PacketReceipt{Hash: []byte("rec-hash")})
	ts.reverseTable[string([]byte("rev-key"))] = &ReverseEntry{
		ReceivedInterface: iface,
		OutboundInterface: iface,
		Timestamp:         time.Now(),
	}
	ts.mu.Unlock()

	ts.Stop()

	ts.mu.Lock()
	receiptsLen := len(ts.receipts)
	reverseLen := len(ts.reverseTable)
	ts.mu.Unlock()
	if receiptsLen != 0 {
		t.Fatalf("receipts len after Stop = %v, want 0 (void_queues)", receiptsLen)
	}
	if reverseLen != 0 {
		t.Fatalf("reverseTable len after Stop = %v, want 0 (void_queues)", reverseLen)
	}
	if got := iface.HeldAnnounces(); got != 0 {
		t.Fatalf("HeldAnnounces after Stop = %v, want 0 (void_queues)", got)
	}
}

// TestPersistDataNonReentrantSkipsConcurrentCall verifies that
// PersistData is guarded by a non-reentrant lock, so a call made while a
// persist is already in flight is skipped (Python Transport.py:3509-3510
// "if persist_lock.locked(): return") rather than queueing or re-entering.
func TestPersistDataNonReentrantSkipsConcurrentCall(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-persist-reentrant-")
	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)
	ts.mu.Lock()
	ts.storagePath = tmpDir
	ts.mu.Unlock()

	// Seed a path entry so PersistData would write destination_table when allowed.
	iface := newBootstrapConstructorTestInterface("reentrant", "TCPServerInterface")
	packet := []byte("announce-packet")
	dest := make([]byte, HashLength/8)
	dest[0] = 0x55
	ts.mu.Lock()
	ts.pathTable[string(dest)] = &PathEntry{
		Timestamp:   time.Now(),
		NextHop:     []byte("hop"),
		Hops:        1,
		Expires:     time.Now().Add(time.Hour),
		RandomBlobs: [][]byte{{0xAA}},
		Interface:   iface,
		Packet:      packet,
		PacketHash:  FullHash(packet),
	}
	ts.mu.Unlock()

	// Hold the persist lock to simulate a persist already in flight.
	ts.persistMu.Lock()

	// A concurrent PersistData must skip (TryLock fails) and write nothing.
	if err := ts.PersistData(); err != nil {
		t.Fatalf("concurrent PersistData returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "destination_table")); err == nil {
		t.Fatal("destination_table written by skipped PersistData; non-reentrant guard failed")
	}

	// Release the in-flight lock; PersistData now runs and writes the table.
	ts.persistMu.Unlock()
	if err := ts.PersistData(); err != nil {
		t.Fatalf("PersistData after unlock returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "destination_table")); err != nil {
		t.Fatalf("destination_table missing after unlocked PersistData: %v", err)
	}
}
