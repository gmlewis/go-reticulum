// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "testing"

// TestEnablingTheHopsDeltaDrawsIt pins the ordering the config layer actually uses. Python
// draws the delta once at Transport.start as `(ord(os.urandom(1))%6)+2`
// (Transport.py:328) because it reads [reticulum] local_hops_delta there. The port applies
// the option from applyConfig, which runs AFTER TransportSystem.Start — so a setter that
// only stored the flag left the delta at 0, and shouldApplyDelta, which requires a
// non-zero delta (Transport.py:1555-1560), could never fire. The disguise was silently
// inert with `local_hops_delta = yes`.
//
// The delta source is injected so the drawn value is deterministic: (3%6)+2 = 5.
func TestEnablingTheHopsDeltaDrawsIt(t *testing.T) {
	t.Parallel()

	ts := topologyTransport(t, "delta-enable", false, false)

	// Start has already run with the option off, so the delta is 0 — exactly the state
	// the config layer was in before it called the setter.
	if got := ts.LocalHopsDelta(); got != 0 {
		t.Fatalf("precondition: delta = %v before enabling, want 0", got)
	}

	ts.SetLocalHopsDeltaRand(func() byte { return 3 })
	ts.SetLocalHopsDeltaEnabled(true)
	if got := ts.LocalHopsDelta(); got != 5 {
		t.Errorf("delta after enabling = %v, want 5 — the setter must draw it, because applyConfig runs after Start", got)
	}

	// Disabling clears it again.
	ts.SetLocalHopsDeltaEnabled(false)
	if got := ts.LocalHopsDelta(); got != 0 {
		t.Errorf("delta after disabling = %v, want 0", got)
	}
}
