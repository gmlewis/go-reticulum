// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// TestNewPathEntryMarkedUnknownState asserts that announcing a
// previously-unknown destination inserts a fresh pathTable entry whose path
// state is "unknown" (ResponsiveState == 0, Unresponsive == false), mirroring
// RNS/Transport.py:2052-2053 where Transport.mark_path_unknown_state is
// invoked immediately after writing the new path_table entry.
func TestNewPathEntryMarkedUnknownState(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	from := newAFI("from-full", interfaces.ModeFull)
	id := mustTestNewIdentity(t, true)
	// Unregistered destination so the announce is non-local and reaches the
	// new-path insertion branch.
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "task8-announce")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	p.Hops = 1
	ts.handleAnnounce(p, from)

	ts.mu.Lock()
	entry, ok := ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatalf("expected a path table entry for %x after announce", dest.Hash)
	}
	if entry.ResponsiveState != 0 {
		t.Errorf("new path ResponsiveState = %v, want 0 (unknown)", entry.ResponsiveState)
	}
	if entry.Unresponsive {
		t.Errorf("new path Unresponsive = true, want false (unknown state)")
	}
}

// TestMarkPathStateNoOpWhenAbsent asserts that MarkPathUnresponsive
// and MarkPathUnknownState are no-ops when the destination has no pathTable
// entry — they neither create an entry nor panic, mirroring Python's
// `if destination_hash in Transport.path_table` guard (Transport.py:2810,
// 2826). MarkPathResponsive is checked for the same contract.
func TestMarkPathStateNoOpWhenAbsent(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	absent := []byte{0xAB, 0xCD, 0xEF}

	before := func() int {
		ts.mu.Lock()
		defer ts.mu.Unlock()
		return len(ts.pathTable)
	}

	n0 := before()
	ts.MarkPathUnresponsive(absent)
	if n1 := before(); n1 != n0 {
		t.Errorf("MarkPathUnresponsive(absent) created an entry: size %v -> %v", n0, n1)
	}
	ts.MarkPathResponsive(absent)
	if n3 := before(); n3 != n0 {
		t.Errorf("MarkPathResponsive(absent) created an entry: size %v -> %v", n0, n3)
	}
	ts.MarkPathUnknownState(absent)
	if n4 := before(); n4 != n0 {
		t.Errorf("MarkPathUnknownState(absent) created an entry: size %v -> %v", n0, n4)
	}
	if ts.PathIsUnresponsive(absent) {
		t.Errorf("PathIsUnresponsive(absent) = true, want false (no entry)")
	}
}
