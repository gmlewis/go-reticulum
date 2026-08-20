// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// recordingAnnounceIface records the first byte of every Send (used as a hop
// tag) in dispatch order, so a test can assert the order in which
// handleOutgoingAnnounces emits its batch.
type recordingAnnounceIface struct {
	dummyInterface
	sentHops []int
}

func (r *recordingAnnounceIface) Send(data []byte) error {
	if len(data) > 0 {
		r.sentHops = append(r.sentHops, int(data[0]))
	}
	return nil
}

// TestHandleOutgoingAnnouncesSortedByHops asserts that
// handleOutgoingAnnounces dispatches a batch of outgoing announces in
// ascending hop order, mirroring RNS/Transport.py:1065-1066
// (`for packet in sorted(outgoing, key=lambda p: p.hops): packet.send()`).
// The hops tag is carried in the first raw byte so the recording interface
// observes the send order deterministically (synchronous dispatch).
func TestHandleOutgoingAnnouncesSortedByHops(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	iface := &recordingAnnounceIface{}
	// Enqueue announces with varying, out-of-order hop counts. The first raw
	// byte tags the hop count the recording interface observes.
	jobs := []outgoingAnnounceJob{
		{iface: iface, raw: []byte{5}, hops: 5},
		{iface: iface, raw: []byte{1}, hops: 1},
		{iface: iface, raw: []byte{3}, hops: 3},
		{iface: iface, raw: []byte{2}, hops: 2},
		{iface: iface, raw: []byte{4}, hops: 4},
	}

	ts.handleOutgoingAnnounces(jobs)

	want := []int{1, 2, 3, 4, 5}
	if len(iface.sentHops) != len(want) {
		t.Fatalf("sentHops = %v, want %v (len mismatch)", iface.sentHops, want)
	}
	for i, h := range iface.sentHops {
		if h != want[i] {
			t.Errorf("sentHops[%v] = %v, want %v (ascending hops order)", i, h, want[i])
		}
	}
}

// TestHandleOutgoingAnnouncesStableEqualHops asserts the sort is stable for
// announces with equal hop counts (sort.SliceStable), preserving enqueue
// order among same-hop announces — matching Python's stable sorted() builtin.
func TestHandleOutgoingAnnouncesStableEqualHops(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	iface := &recordingAnnounceIface{}
	// Two announces at hops=2 carry distinct tags (10, 20) in enqueue order;
	// after sorting by hops they must remain in that relative order.
	jobs := []outgoingAnnounceJob{
		{iface: iface, raw: []byte{30}, hops: 3},
		{iface: iface, raw: []byte{10}, hops: 2},
		{iface: iface, raw: []byte{20}, hops: 2},
		{iface: iface, raw: []byte{40}, hops: 4},
	}

	ts.handleOutgoingAnnounces(jobs)

	want := []int{10, 20, 30, 40}
	if len(iface.sentHops) != len(want) {
		t.Fatalf("sentHops = %v, want %v (len mismatch)", iface.sentHops, want)
	}
	for i, h := range iface.sentHops {
		if h != want[i] {
			t.Errorf("sentHops[%v] = %v, want %v (stable ascending order)", i, h, want[i])
		}
	}
}

// ensure interfaces import is used even if the file grows only mock helpers.
var _ = interfaces.ModeFull
