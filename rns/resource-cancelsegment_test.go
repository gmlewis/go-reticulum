// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
)

// TestResourceCancelCascadesToNextSegment asserts Phase 9 Task 1: cancelling a
// multi-segment outgoing resource recursively cancels its pending next segment
// before tearing down the current segment (Python Resource.cancel,
// Resource.py:1087-1088: `if self.next_segment: self.next_segment.cancel()`).
// The cascaded cancel must prevent the next segment from ever being advertised
// — it ends up FAILED with no advertisement packet. The two resources here
// stand in for the chain that Python's __prepare_next_segment would build
// during a multi-segment transfer; the cancel recursion itself is what is under
// test, independent of the (separate) segment-splitting machinery.
func TestResourceCancelCascadesToNextSegment(t *testing.T) {
	t.Parallel()
	link := testActiveResourceLink(t)
	link.logger = testSilentLogger()

	r1 := mustTestNewResourceWithOptions(t, bytes.Repeat([]byte{0xA1}, 500), link, ResourceOptions{})
	r2 := mustTestNewResourceWithOptions(t, bytes.Repeat([]byte{0xA2}, 500), link, ResourceOptions{})

	// Chain the segments as __prepare_next_segment would. Both are queued and
	// have not advertised yet.
	r1.nextSegment = r2

	if got := r2.Status(); got != ResourceStatusQueued {
		t.Fatalf("r2 pre-cancel status = %v, want Queued", got)
	}

	r1.Cancel()

	if got := r1.Status(); got != ResourceStatusFailed {
		t.Errorf("r1 status after cancel = %v, want Failed", got)
	}
	// The recursive cancel must have cascaded to the next segment.
	if got := r2.Status(); got != ResourceStatusFailed {
		t.Errorf("r2 (nextSegment) status after r1.Cancel = %v, want Failed (cascade)", got)
	}
	// A cancelled next segment never advertises: no advertisement packet is
	// built and its status never reaches Advertised.
	if r2.advertisementPacket != nil {
		t.Error("r2 advertisementPacket is set; want nil (cascade prevents advertisement)")
	}
	if r2.Status() == ResourceStatusAdvertised {
		t.Error("r2 status reached Advertised; want it cancelled before advertising")
	}
}

// TestResourceCancelNoNextSegment asserts the no-next-segment path: cancelling
// a single-segment resource (nextSegment == nil) is a no-op for the recursion
// and simply fails the resource, mirroring Python's `if self.next_segment`
// guard (Resource.py:1087).
func TestResourceCancelNoNextSegment(t *testing.T) {
	t.Parallel()
	link := testActiveResourceLink(t)
	link.logger = testSilentLogger()

	r1 := mustTestNewResourceWithOptions(t, bytes.Repeat([]byte{0xA3}, 500), link, ResourceOptions{})

	r1.Cancel()

	if got := r1.Status(); got != ResourceStatusFailed {
		t.Errorf("r1 status after cancel = %v, want Failed", got)
	}
}
