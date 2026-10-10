// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
)

// testAcceptedAdvertisement builds a real resource advertisement for a transfer
// of data and returns it addressed to the receiving link, so a test can drive
// Accept exactly as Link.receive does for a response resource.
func testAcceptedAdvertisement(t *testing.T, receiver *Link, data []byte) *Packet {
	t.Helper()

	sender := testActiveResourceLink(t)
	r, err := NewResourceWithOptions(data, sender, ResourceOptions{})
	if err != nil {
		t.Fatalf("NewResourceWithOptions: %v", err)
	}
	adv, err := r.buildAdvertisementPacket()
	if err != nil {
		t.Fatalf("buildAdvertisementPacket: %v", err)
	}
	adv.Destination = receiver
	return adv
}

// TestAcceptIgnoresRepeatAdvertisement pins that one transfer registers one
// incoming resource on the receiving link, even when the advertisement arrives
// more than once.
//
// A sender re-advertises a resource it believes has not started: its
// advertisement watchdog fires, or the receiver's first request is lost on a
// multi-hop path. Python's Link.receive accepts every advertisement it sees
// (Link.py:1048) and this port did the same, so the receiving link ended up with
// a resource per copy. Because every delivered part makes every registered
// receiver request more parts, the copies multiply the traffic instead of
// converging: a page fetch from a two-hop node was measured registering up to
// five resources for one hash and stalling short of the whole page while tens
// of thousands of duplicate parts crossed the link.
//
// The resource hash covers the payload together with the transfer's random
// hash, so an equal hash is the same data from the same sender and there is
// nothing to gain from a second receiver.
func TestAcceptIgnoresRepeatAdvertisement(t *testing.T) {
	t.Parallel()

	receiver := testActiveResourceLink(t)
	data := make([]byte, 4096)

	// The SAME advertisement again: a sender's advertisement watchdog resends
	// the very packet it built for the transfer, so the resource and random
	// hashes match exactly.
	repeat := testAcceptedAdvertisement(t, receiver, data)

	first, err := Accept(repeat, nil, nil, nil)
	if err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if first == nil {
		t.Fatal("first Accept returned no resource")
	}

	second, err := Accept(repeat, nil, nil, nil)
	if err != nil {
		t.Fatalf("repeat Accept: %v", err)
	}
	if second != first {
		t.Error("a repeat advertisement for a transfer already in flight registered a second resource")
	}

	receiver.mu.Lock()
	count := len(receiver.incomingResources)
	receiver.mu.Unlock()
	if count != 1 {
		t.Errorf("incoming resources after a repeat advertisement = %v, want 1", count)
	}
}

// TestAcceptKeepsDistinctTransfers pins the other side of that guard: two
// genuine transfers of the same payload have different random hashes and so
// different resource hashes, and both must be accepted.
func TestAcceptKeepsDistinctTransfers(t *testing.T) {
	t.Parallel()

	receiver := testActiveResourceLink(t)
	data := make([]byte, 4096)

	if _, err := Accept(testAcceptedAdvertisement(t, receiver, data), nil, nil, nil); err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if _, err := Accept(testAcceptedAdvertisement(t, receiver, data), nil, nil, nil); err != nil {
		t.Fatalf("second Accept: %v", err)
	}

	receiver.mu.Lock()
	count := len(receiver.incomingResources)
	receiver.mu.Unlock()
	// The identical payload is advertised with a fresh random hash each time, so
	// these are two different transfers and the first must not suppress the
	// second. Anything else would swallow a second fetch of the same page.
	if count < 2 {
		t.Errorf("incoming resources after two distinct transfers = %v, want at least 2", count)
	}
}
