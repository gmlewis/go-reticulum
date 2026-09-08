// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync"
	"testing"
	"time"
)

// newLinkTableProbe builds a TransportSystem holding a couple of link entries,
// as the forwarding path and janitor would leave behind.
func newLinkTableProbe() *TransportSystem {
	ts := &TransportSystem{linkTable: make(map[string]*LinkEntry)}
	ts.linkTable["aaaabbcc00112233445566778899aabb"] = &LinkEntry{Timestamp: time.Now(), RemainingHops: 1}
	ts.linkTable["bbbbccdd00112233445566778899aacc"] = &LinkEntry{Timestamp: time.Now(), RemainingHops: 2}
	return ts
}

// TestLinkTableReturnsSnapshot verifies LinkTable() returns a copy of the
// internal link table, not the live map: the live map is mutated by the
// forwarding path and the stale-table janitor under ts.mu, so a caller
// iterating a leaked live map races those writers and is a fatal
// `concurrent map read and map write` for the whole process.
func TestLinkTableReturnsSnapshot(t *testing.T) {
	t.Parallel()

	ts := newLinkTableProbe()
	before := ts.LinkTable()
	if len(before) != 2 {
		t.Fatalf("LinkTable() = %d entries, want 2", len(before))
	}

	// Mutating the returned map must not touch the internal table.
	for k := range before {
		delete(before, k)
	}
	if after := ts.LinkTable(); len(after) != 2 {
		t.Fatalf("internal linkTable = %d entries after the returned snapshot was mutated, want 2 (LinkTable() leaked the live map)",
			len(after))
	}

	// Mutating the internal table (as the janitor does) must not be visible
	// through a previously returned snapshot.
	snap := ts.LinkTable()
	ts.mu.Lock()
	delete(ts.linkTable, "aaaabbcc00112233445566778899aabb")
	ts.mu.Unlock()
	if _, alive := snap["aaaabbcc00112233445566778899aabb"]; !alive {
		t.Fatal("previously returned snapshot lost an entry when the internal table changed")
	}
}

// TestLinkTableSnapshotNoRace exercises the snapshot under the race detector:
// one goroutine mutates the internal table (as the forwarding path and janitor
// do, under ts.mu) while another repeatedly fetches and iterates LinkTable().
// With the pre-fix live-map leak this is a fatal `concurrent map read and map
// write` (or a -race report); with the snapshot it is clean.
func TestLinkTableSnapshotNoRace(t *testing.T) {
	t.Parallel()

	ts := newLinkTableProbe()
	ts.linkTable["ccccddee00112233445566778899aadd"] = &LinkEntry{Timestamp: time.Now(), RemainingHops: 3}

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 2000 {
			ts.mu.Lock()
			ts.linkTable["ddddeeff00112233445566778899aaee"] = &LinkEntry{Timestamp: time.Now()}
			delete(ts.linkTable, "ddddeeff00112233445566778899aaee")
			ts.mu.Unlock()
		}
	})

	for range 2000 {
		for _, entry := range ts.LinkTable() {
			_ = entry.Timestamp
		}
	}
	wg.Wait()
}
