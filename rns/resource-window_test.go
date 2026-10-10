// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
)

// testReceivingResource builds a receiver-side resource whose hashmap is
// derived from real part data, so ReceivePart matches the parts a test feeds
// it. initiator is set true only so RequestNext becomes a no-op: the request
// goroutine would otherwise race the assertions on this resource's window and
// outstanding-request bookkeeping.
func testReceivingResource(t *testing.T, partData [][]byte) *Resource {
	t.Helper()

	link := testActiveResourceLink(t)
	r, err := NewResourceWithOptions(partData[0], link, ResourceOptions{AutoCompress: false})
	if err != nil {
		t.Fatalf("NewResourceWithOptions: %v", err)
	}
	r.initiator = true
	r.status = ResourceStatusTransferring

	r.hashmap = make([][]byte, len(partData))
	r.parts = make([]*ResourcePart, len(partData))
	for i, d := range partData {
		mapHash := r.getMapHash(d)
		r.hashmap[i] = mapHash
		r.parts[i] = &ResourcePart{Index: i, MapHash: mapHash}
	}
	r.totalParts = len(partData)
	r.window = 2
	r.windowMax = 10
	r.windowMin = 2
	r.windowFlexibility = ResourceWindowFlexibility
	return r
}

func testPartData(n int, size int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		part := bytes.Repeat([]byte{byte(i + 1)}, size)
		out[i] = part
	}
	return out
}

// TestReceivePartGrowsWindowOnlyWhenNoRequestIsOutstanding pins Python's
// receive_part guard and window growth (Resource.py:903-909): a new part
// request goes out only when no request is already in flight, and each clean
// round lets the window grow toward its maximum.
//
// Requesting after every single received part — as this port did — multiplies
// the request traffic by the number of parts in flight, and each extra request
// makes the sender re-send parts it has already sent. The link fills with
// duplicates, the receiver's watchdog shrinks the window to its minimum to
// recover, and nothing ever grows it back, so the transfer then advances at two
// parts per backoff round: a page that should cross a two-hop path in a fraction
// of a second was measured taking 138 seconds.
func TestReceivePartGrowsWindowOnlyWhenNoRequestIsOutstanding(t *testing.T) {
	t.Parallel()

	parts := testPartData(8, 64)
	r := testReceivingResource(t, parts)
	r.outstandingParts = 2

	if err := r.ReceivePart(&Packet{Data: parts[0]}); err != nil {
		t.Fatalf("ReceivePart(0): %v", err)
	}
	r.mu.Lock()
	afterFirst, windowAfterFirst := r.outstandingParts, r.window
	r.mu.Unlock()
	if afterFirst != 1 {
		t.Errorf("outstandingParts after one of two requested parts = %v, want 1", afterFirst)
	}
	if windowAfterFirst != 2 {
		t.Errorf("window grew to %v while a request was still outstanding, want it left at 2", windowAfterFirst)
	}

	if err := r.ReceivePart(&Packet{Data: parts[1]}); err != nil {
		t.Fatalf("ReceivePart(1): %v", err)
	}
	r.mu.Lock()
	afterSecond, windowAfterSecond := r.outstandingParts, r.window
	r.mu.Unlock()
	if afterSecond != 0 {
		t.Errorf("outstandingParts after both requested parts = %v, want 0", afterSecond)
	}
	if windowAfterSecond != 3 {
		t.Errorf("window after a clean round = %v, want 3 (Python Resource.py:905 grows it once no request is outstanding)", windowAfterSecond)
	}
}

// TestReceivePartIgnoresDuplicatePartsForWindowGrowth pins that a part arriving
// twice does not count as progress: the duplicate must not decrement the
// outstanding-request count nor grow the window.
func TestReceivePartIgnoresDuplicatePartsForWindowGrowth(t *testing.T) {
	t.Parallel()

	parts := testPartData(8, 64)
	r := testReceivingResource(t, parts)
	r.outstandingParts = 2

	if err := r.ReceivePart(&Packet{Data: parts[0]}); err != nil {
		t.Fatalf("ReceivePart(0): %v", err)
	}
	if err := r.ReceivePart(&Packet{Data: parts[0]}); err != nil {
		t.Fatalf("ReceivePart(0) duplicate: %v", err)
	}

	r.mu.Lock()
	outstanding, window, received := r.outstandingParts, r.window, r.receivedCount
	r.mu.Unlock()
	if received != 1 {
		t.Errorf("receivedCount after a duplicate part = %v, want 1", received)
	}
	if outstanding != 1 {
		t.Errorf("outstandingParts after a duplicate part = %v, want 1 (a duplicate is not progress)", outstanding)
	}
	if window != 2 {
		t.Errorf("window after a duplicate part = %v, want 2", window)
	}
}
