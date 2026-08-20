// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// blockingPathIface is a test Interface whose Name() blocks on a gate channel
// until the test releases it, and signals an entered channel each time Name()
// is called. It lets a test pause pathTableSnapshot mid-serialization (after
// the per-entry lock is released, while interfaceHash computes) so the test
// can delete other entries concurrently without racing the lock.
type blockingPathIface struct {
	*interfaces.BaseInterface
	ifaceType string
	gate      chan struct{}
	entered   chan struct{}
}

func newBlockingPathIface(name string, gate, entered chan struct{}) *blockingPathIface {
	return &blockingPathIface{
		BaseInterface: interfaces.NewBaseInterface(name, interfaces.ModeFull, 0),
		ifaceType:     "TCPServerInterface",
		gate:          gate,
		entered:       entered,
	}
}

func (i *blockingPathIface) Name() string {
	// Signal that serialization has reached this interface's hash computation
	// (the persist holds no lock here), then block until the test opens the gate.
	select {
	case i.entered <- struct{}{}:
	default:
	}
	<-i.gate
	return i.BaseInterface.Name()
}

func (i *blockingPathIface) Type() string      { return i.ifaceType }
func (i *blockingPathIface) Status() bool      { return true }
func (i *blockingPathIface) IsOut() bool       { return true }
func (i *blockingPathIface) Send([]byte) error { return nil }
func (i *blockingPathIface) Detach() error {
	i.SetDetached(true)
	return nil
}

// TestPathTableSnapshotSkipsEntryRemovedMidPersist verifies that
// pathTableSnapshot re-checks each entry against the live pathTable under the
// lock before serializing, so an entry removed concurrently mid-save is
// skipped (Python Transport.py:3370 "no longer in table" intent) instead of
// panicking on a nil lookup.
func TestPathTableSnapshotSkipsEntryRemovedMidPersist(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	testutils.TempDir(t, "rns-pathtable-midpersist-") // reserved; no file I/O needed

	gate := make(chan struct{})
	entered := make(chan struct{}, 3)

	addEntry := func(name string, destByte byte) {
		iface := newBlockingPathIface(name, gate, entered)
		dest := make([]byte, HashLength/8)
		dest[0] = destByte
		packet := []byte("packet-" + name)
		entry := &PathEntry{
			Timestamp:   time.Now(),
			NextHop:     []byte("hop"),
			Hops:        1,
			Expires:     time.Now().Add(time.Hour),
			RandomBlobs: [][]byte{{0xAA}},
			Interface:   iface,
			Packet:      packet,
			PacketHash:  FullHash(packet),
		}
		ts.mu.Lock()
		ts.pathTable[string(dest)] = entry
		ts.mu.Unlock()
	}
	addEntry("A", 0x01)
	addEntry("B", 0x02)
	addEntry("C", 0x03)

	type result struct {
		snapshot []any
		caches   []pathCacheItem
		panicked bool
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- result{panicked: true}
			}
		}()
		snap, caches := ts.pathTableSnapshot()
		done <- result{snapshot: snap, caches: caches}
	}()

	// Wait until the persist has entered the first interface's Name() — i.e.
	// it is mid-serialization, past that entry's recheck, holding no lock.
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pathTableSnapshot never entered interfaceHash within 5s")
	}

	// Concurrently remove every entry from the live path table. The entry
	// currently being serialized is already past its recheck so it survives;
	// the not-yet-serialized entries must be re-checked and skipped.
	ts.mu.Lock()
	for k := range ts.pathTable {
		delete(ts.pathTable, k)
	}
	ts.mu.Unlock()

	// Release the blocking Name() so serialization completes.
	close(gate)

	select {
	case res := <-done:
		if res.panicked {
			t.Fatal("pathTableSnapshot panicked on a concurrently removed entry")
		}
		if got, want := len(res.snapshot), 1; got != want {
			t.Fatalf("snapshot len = %v, want %v (only the in-flight entry survives)", got, want)
		}
		if got, want := len(res.caches), 1; got != want {
			t.Fatalf("caches len = %v, want %v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pathTableSnapshot did not return within 5s (deadlock)")
	}
}

// TestPathTableSnapshotNoConcurrentRemovalIsConsistent verifies that
// with no concurrent mutation, pathTableSnapshot serializes every eligible
// entry — the recheck never skips — so the destination_table stays complete.
func TestPathTableSnapshotNoConcurrentRemovalIsConsistent(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	addEntry := func(name string, destByte byte) {
		iface := newBootstrapConstructorTestInterface(name, "TCPServerInterface")
		dest := make([]byte, HashLength/8)
		dest[0] = destByte
		packet := []byte("packet-" + name)
		entry := &PathEntry{
			Timestamp:   time.Now(),
			NextHop:     []byte("hop"),
			Hops:        1,
			Expires:     time.Now().Add(time.Hour),
			RandomBlobs: [][]byte{{0xAA}},
			Interface:   iface,
			Packet:      packet,
			PacketHash:  FullHash(packet),
		}
		ts.mu.Lock()
		ts.pathTable[string(dest)] = entry
		ts.mu.Unlock()
	}
	addEntry("A", 0x01)
	addEntry("B", 0x02)
	addEntry("C", 0x03)

	snapshot, caches := ts.pathTableSnapshot()
	if got, want := len(snapshot), 3; got != want {
		t.Fatalf("snapshot len = %v, want %v", got, want)
	}
	if got, want := len(caches), 3; got != want {
		t.Fatalf("caches len = %v, want %v", got, want)
	}
}
