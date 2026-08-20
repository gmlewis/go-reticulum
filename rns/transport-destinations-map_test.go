// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"testing"
)

// TestDestinationsMapHashLookup verifies that registering N
// destinations populates a hash-keyed destinationsMap, and
// localDestinationLocked resolves a destination by its hash in O(1) instead
// of scanning the destinations list (Python Transport.destinations_map,
// Transport.py:104,1216-1218).
func TestDestinationsMapHashLookup(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	id := mustTestNewIdentity(t, true)

	const n = 5
	dests := make([]*Destination, n)
	for i := range n {
		dests[i] = mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "app", fmt.Sprintf("d%d", i))
	}

	ts.mu.Lock()
	if got, want := len(ts.destinationsMap), n; got != want {
		t.Fatalf("destinationsMap len = %v, want %v", got, want)
	}
	for _, d := range dests {
		got := ts.localDestinationLocked(d.Hash)
		if got != d {
			t.Fatalf("localDestinationLocked(%x) returned %p, want %p (lookup must be by hash)", d.Hash, got, d)
		}
	}
	// An unknown hash resolves to nil.
	unknown := make([]byte, len(dests[0].Hash))
	if got := ts.localDestinationLocked(unknown); got != nil {
		t.Fatalf("localDestinationLocked(unknown) = %p, want nil", got)
	}
	ts.mu.Unlock()
}

// TestCleanDestinationsMapReconciles verifies that the reconcile
// job re-adds a registered destination missing from the map and drops a stale
// map entry whose destination is no longer registered (Python
// Transport.clean_destinations_map, Transport.py:2478-2496).
func TestCleanDestinationsMapReconciles(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	id := mustTestNewIdentity(t, true)
	d1 := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "app", "r1")
	d2 := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "app", "r2")

	// Simulate map drift by bypassing the register/deregister maintenance.
	ts.mu.Lock()
	delete(ts.destinationsMap, string(d1.Hash))
	ts.destinationsMap[string([]byte("stale"))] = d2
	ts.mu.Unlock()

	ts.CleanDestinationsMap()

	ts.mu.Lock()
	if _, ok := ts.destinationsMap[string(d1.Hash)]; !ok {
		t.Fatal("CleanDestinationsMap did not re-add the missing registered destination")
	}
	if _, ok := ts.destinationsMap[string([]byte("stale"))]; ok {
		t.Fatal("CleanDestinationsMap did not drop the stale map entry")
	}
	if got, want := len(ts.destinationsMap), 2; got != want {
		t.Fatalf("destinationsMap len after reconcile = %v, want %v", got, want)
	}
	ts.mu.Unlock()
}

// TestDeregisterDestinationRemovesFromMap verifies that deregister
// removes the destination from both the list and the hash map.
func TestDeregisterDestinationRemovesFromMap(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	id := mustTestNewIdentity(t, true)
	d := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "app", "rm")

	ts.DeregisterDestination(d)

	ts.mu.Lock()
	_, inMap := ts.destinationsMap[string(d.Hash)]
	inList := false
	for _, existing := range ts.destinations {
		if existing == d {
			inList = true
		}
	}
	ts.mu.Unlock()
	if inMap {
		t.Fatal("deregistered destination still in destinationsMap")
	}
	if inList {
		t.Fatal("deregistered destination still in destinations list")
	}
}
