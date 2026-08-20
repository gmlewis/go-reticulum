// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"sync"
	"testing"
)

// TestResourceProgressCallbackCascadesToNextSegment asserts that
// SetProgressCallback forwards the callback to nextSegment when one exists,
// so a multi-segment transfer reports progress from every segment (Python
// Resource.progress_callback, Resource.py:1136-1138). Setting the callback on
// the head of a three-segment chain installs it on every segment, and firing
// each segment's installed callback runs the shared callback once per segment.
func TestResourceProgressCallbackCascadesToNextSegment(t *testing.T) {
	t.Parallel()
	link := testActiveResourceLink(t)
	link.logger = testSilentLogger()
	r1 := mustTestNewResourceWithOptions(t, bytes.Repeat([]byte{0xB1}, 500), link, ResourceOptions{})
	r2 := mustTestNewResourceWithOptions(t, bytes.Repeat([]byte{0xB2}, 500), link, ResourceOptions{})
	r3 := mustTestNewResourceWithOptions(t, bytes.Repeat([]byte{0xB3}, 500), link, ResourceOptions{})
	r1.nextSegment = r2
	r2.nextSegment = r3

	var mu sync.Mutex
	fires := 0
	cb := func(r *Resource) {
		mu.Lock()
		fires++
		mu.Unlock()
	}
	r1.SetProgressCallback(cb)

	// The cascade must install the same callback on every segment, and the
	// callback must fire once per segment.
	for i, r := range []*Resource{r1, r2, r3} {
		r.mu.Lock()
		pcb := r.progressCallback
		r.mu.Unlock()
		if pcb == nil {
			t.Fatalf("segment %d progressCallback not installed (cascade failed)", i)
		}
		pcb(r)
	}
	mu.Lock()
	got := fires
	mu.Unlock()
	if got != 3 {
		t.Errorf("progress callback fired %d times, want 3 (once per segment)", got)
	}
}

// TestResourceProgressCallbackNoNextSegment asserts the no-next-segment path:
// SetProgressCallback on a single-segment resource installs the callback on
// that resource only (Python `if self.next_segment` guard, Resource.py:1137),
// and firing it runs exactly once.
func TestResourceProgressCallbackNoNextSegment(t *testing.T) {
	t.Parallel()
	link := testActiveResourceLink(t)
	link.logger = testSilentLogger()
	r1 := mustTestNewResourceWithOptions(t, bytes.Repeat([]byte{0xB4}, 500), link, ResourceOptions{})

	var mu sync.Mutex
	fires := 0
	cb := func(r *Resource) {
		mu.Lock()
		fires++
		mu.Unlock()
	}
	r1.SetProgressCallback(cb)

	r1.mu.Lock()
	pcb := r1.progressCallback
	r1.mu.Unlock()
	if pcb == nil {
		t.Fatal("r1 progressCallback not installed")
	}
	pcb(r1)
	mu.Lock()
	got := fires
	mu.Unlock()
	if got != 1 {
		t.Errorf("progress callback fired %d times, want 1", got)
	}
}
