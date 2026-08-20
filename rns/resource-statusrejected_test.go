// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestResourceStatusRejectedValue asserts that the Go
// ResourceStatusRejected constant equals Python's live Resource.REJECTED
// (Resource.py:152) for wire-format parity, and that the related status
// constants (None, Corrupt, Complete, Assembling) match Python too. The
// previous Go value (0x00) collided with ResourceStatusNone, so this also
// asserts the two are distinct and that Rejected sits above Corrupt as a
// terminal state — preserving the status ordering that gate comparisons
// (status < COMPLETE, status < ASSEMBLING) rely on. The expected values are
// captured live from `import RNS` rather than hardcoded.
func TestResourceStatusRejectedValue(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	pyRejected, pyNone, pyCorrupt, pyComplete, pyAssembling := pythonResourceStatusConstants(t)

	if ResourceStatusRejected != pyRejected {
		t.Errorf("ResourceStatusRejected = %#02x, want live Python %#02x (Resource.REJECTED)", ResourceStatusRejected, pyRejected)
	}
	if ResourceStatusNone != pyNone {
		t.Errorf("ResourceStatusNone = %#02x, want live Python %#02x", ResourceStatusNone, pyNone)
	}
	if ResourceStatusCorrupt != pyCorrupt {
		t.Errorf("ResourceStatusCorrupt = %#02x, want live Python %#02x", ResourceStatusCorrupt, pyCorrupt)
	}
	if ResourceStatusComplete != pyComplete {
		t.Errorf("ResourceStatusComplete = %#02x, want live Python %#02x", ResourceStatusComplete, pyComplete)
	}
	if ResourceStatusAssembling != pyAssembling {
		t.Errorf("ResourceStatusAssembling = %#02x, want live Python %#02x", ResourceStatusAssembling, pyAssembling)
	}

	// Ordering invariants (follow from the live-verified values): Rejected
	// must not collide with None and must be a terminal state above Corrupt.
	if ResourceStatusRejected == ResourceStatusNone {
		t.Errorf("ResourceStatusRejected (0x%02x) collides with ResourceStatusNone (0x%02x); want distinct", ResourceStatusRejected, ResourceStatusNone)
	}
	if ResourceStatusRejected <= ResourceStatusCorrupt {
		t.Errorf("ResourceStatusRejected = 0x%02x, want > 0x%02x (Corrupt) so it is a terminal state above Corrupt", ResourceStatusRejected, ResourceStatusCorrupt)
	}
}
