// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "testing"

// TestResourceStatusRejectedValue asserts Phase 9 Task 4: the
// ResourceStatusRejected constant is 0x09, matching Python
// Resource.REJECTED (Resource.py:152) for wire-format parity. The previous
// Go value (0x00) collided with ResourceStatusNone, so the bump also asserts
// the two are now distinct and that Rejected sits above Corrupt (0x08) as a
// terminal state — preserving the status ordering that gate comparisons
// (status < COMPLETE, status < ASSEMBLING) rely on.
func TestResourceStatusRejectedValue(t *testing.T) {
	t.Parallel()
	if ResourceStatusRejected != 0x09 {
		t.Errorf("ResourceStatusRejected = %#02x, want 0x09 (Python Resource.REJECTED)", ResourceStatusRejected)
	}
	if ResourceStatusRejected == ResourceStatusNone {
		t.Errorf("ResourceStatusRejected (0x%02x) collides with ResourceStatusNone (0x%02x); want distinct", ResourceStatusRejected, ResourceStatusNone)
	}
	if ResourceStatusRejected <= ResourceStatusCorrupt {
		t.Errorf("ResourceStatusRejected = 0x%02x, want > 0x%02x (Corrupt) so it is a terminal state above Corrupt", ResourceStatusRejected, ResourceStatusCorrupt)
	}
}
