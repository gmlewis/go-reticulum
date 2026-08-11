// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"
)

// sentContexts returns the Context field of every packet the capture transport
// has recorded, in send order.
func sentContexts(ct *captureTransport) []int {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	out := make([]int, len(ct.sent))
	for i, p := range ct.sent {
		out[i] = p.Context
	}
	return out
}

// containsContext reports whether the capture transport recorded a packet with
// the given Context.
func containsContext(ct *captureTransport, want int) bool {
	for _, c := range sentContexts(ct) {
		if c == want {
			return true
		}
	}
	return false
}

// TestResourceCancelInitiatorSendsICL asserts Phase 9 Task 3: cancelling an
// outgoing (initiator) resource below COMPLETE sends a RESOURCE_ICL packet
// when the link is active, removes the resource from the link's outgoing
// list, marks it FAILED, and fires the callback (Python Resource.cancel,
// Resource.py:1095-1104). The initiator sends RESOURCE_ICL
// (RNS.Packet.RESOURCE_ICL) — not RESOURCE_RCL — so the receiver knows the
// sender abandoned the transfer.
func TestResourceCancelInitiatorSendsICL(t *testing.T) {
	t.Parallel()
	ct := newCaptureTransport()
	link := testActiveResourceLink(t)
	link.transport = ct
	link.logger = testSilentLogger()

	r := mustTestNewResourceWithOptions(t, bytes.Repeat([]byte{0xA1}, 500), link, ResourceOptions{})
	link.outgoingResources = append(link.outgoingResources, r)

	fired := make(chan struct{}, 1)
	r.SetCallback(func(*Resource) { fired <- struct{}{} })

	r.Cancel()

	if got := r.Status(); got != ResourceStatusFailed {
		t.Errorf("initiator status after cancel = %v, want Failed", got)
	}
	for _, existing := range link.outgoingResources {
		if existing == r {
			t.Error("resource was not removed from link.outgoingResources")
		}
	}
	if !containsContext(ct, ContextResourceIcl) {
		t.Errorf("no RESOURCE_ICL cancel packet sent; contexts = %v", sentContexts(ct))
	}
	if containsContext(ct, ContextResourceRcl) {
		t.Errorf("initiator cancel sent RESOURCE_RCL; want RESOURCE_ICL only (contexts = %v)", sentContexts(ct))
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("callback did not fire on initiator cancel")
	}
}

// TestResourceCancelReceiverSendsRCL asserts Phase 9 Task 3: cancelling an
// incoming (receiver) resource below COMPLETE sends a RESOURCE_RCL packet
// when the link is active, removes the resource from the link's incoming
// list, marks it FAILED, and fires the callback (Python Resource.cancel,
// Resource.py:1105-1114). The receiver sends RESOURCE_RCL.
func TestResourceCancelReceiverSendsRCL(t *testing.T) {
	t.Parallel()
	ct := newCaptureTransport()
	link := testActiveResourceLink(t)
	link.transport = ct
	link.logger = testSilentLogger()

	r := &Resource{
		link:                link,
		initiator:           false,
		hash:                bytes.Repeat([]byte{0xCD}, 32),
		status:              ResourceStatusTransferring,
		size:                500,
		startedTransferring: time.Now().Add(-time.Second),
	}
	link.incomingResources = append(link.incomingResources, r)

	fired := make(chan struct{}, 1)
	r.SetCallback(func(*Resource) { fired <- struct{}{} })

	r.Cancel()

	if got := r.Status(); got != ResourceStatusFailed {
		t.Errorf("receiver status after cancel = %v, want Failed", got)
	}
	for _, existing := range link.incomingResources {
		if existing == r {
			t.Error("resource was not removed from link.incomingResources")
		}
	}
	if !containsContext(ct, ContextResourceRcl) {
		t.Errorf("no RESOURCE_RCL cancel packet sent; contexts = %v", sentContexts(ct))
	}
	if containsContext(ct, ContextResourceIcl) {
		t.Errorf("receiver cancel sent RESOURCE_ICL; want RESOURCE_RCL only (contexts = %v)", sentContexts(ct))
	}
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("callback did not fire on receiver cancel")
	}
}

// TestResourceCancelCorruptTearsDownLink asserts Phase 9 Task 3's CORRUPT
// branch (Python Resource.cancel, Resource.py:1090-1093): cancelling a
// resource already marked CORRUPT runs cancel_incoming_resource +
// reject(advertisement) + link.teardown. The link ends up LinkClosed and the
// resource is removed from the incoming list, while its status stays Corrupt
// (the CORRUPT branch does not flip it to FAILED). This mirrors the
// decompression-bomb guard's CORRUPT path but exercises it through Cancel
// rather than Assemble. linkID is left empty so the teardown packet itself is
// a no-op (sendTeardownPacket guards on a non-empty linkID); the teardown
// still flips the link to LinkClosed.
func TestResourceCancelCorruptTearsDownLink(t *testing.T) {
	t.Parallel()
	ct := newCaptureTransport()
	link := testActiveResourceLink(t)
	link.transport = ct
	link.logger = testSilentLogger()

	r := &Resource{
		link:                link,
		initiator:           false,
		hash:                bytes.Repeat([]byte{0xCE}, 32),
		status:              ResourceStatusCorrupt,
		advertisementPacket: &Packet{Data: []byte("not-an-advertisement")},
	}
	link.incomingResources = append(link.incomingResources, r)

	r.Cancel()

	if got := link.GetStatus(); got != LinkClosed {
		t.Errorf("link status = %v, want LinkClosed (CORRUPT cancel tears down the link)", got)
	}
	for _, existing := range link.incomingResources {
		if existing == r {
			t.Error("corrupt resource was not removed from link.incomingResources")
		}
	}
	if got := r.Status(); got != ResourceStatusCorrupt {
		t.Errorf("corrupt resource status after cancel = %v, want Corrupt (unchanged)", got)
	}
}

// TestResourceCancelCompleteIsNoOp asserts that cancelling an already-complete
// resource is a no-op: Python's cancel has no branch for status >= COMPLETE,
// so a complete resource is not flipped to FAILED, sends no cancel packet,
// and fires no callback.
func TestResourceCancelCompleteIsNoOp(t *testing.T) {
	t.Parallel()
	ct := newCaptureTransport()
	link := testActiveResourceLink(t)
	link.transport = ct
	link.logger = testSilentLogger()

	r := mustTestNewResourceWithOptions(t, bytes.Repeat([]byte{0xA1}, 500), link, ResourceOptions{})
	r.status = ResourceStatusComplete
	link.outgoingResources = append(link.outgoingResources, r)

	fired := make(chan struct{}, 1)
	r.SetCallback(func(*Resource) { fired <- struct{}{} })

	r.Cancel()

	if got := r.Status(); got != ResourceStatusComplete {
		t.Errorf("complete resource status after cancel = %v, want Complete (no-op)", got)
	}
	if len(ct.sent) != 0 {
		t.Errorf("complete resource sent %d packets; want 0 (contexts = %v)", len(ct.sent), sentContexts(ct))
	}
	select {
	case <-fired:
		t.Error("callback fired for a complete resource; want no callback")
	case <-time.After(50 * time.Millisecond):
	}
}
