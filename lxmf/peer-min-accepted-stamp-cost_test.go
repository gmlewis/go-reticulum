// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import "testing"

// TestMinAcceptedStampCostAppliesMaxLowerBound covers Phase 17 task 5: the
// minimum accepted proof-of-work stamp cost for offers to a peer is
// max(0, propagation_stamp_cost - propagation_stamp_cost_flexibility),
// mirroring Python's
// `min_accepted_cost = max(0, self.propagation_stamp_cost-
// self.propagation_stamp_cost_flexibility)` (LXMPeer.py:331, v1.1.0). The
// max(0, ...) lower bound is what stops the offer-preparation branch from
// accepting a negative cost when flexibility exceeds the advertised cost;
// the validation path already applies the same bound on the router side
// (router.go `max(r.propagationCost-r.propagationCostFlexibility, 0)`).
func TestMinAcceptedStampCostAppliesMaxLowerBound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		cost     *int
		flex     *int
		wantCost int
		wantOK   bool
	}{
		{"cost_above_flex", new(16), new(3), 13, true},
		{"flex_exceeds_cost_clamps_to_zero", new(3), new(5), 0, true},
		{"flex_equals_cost", new(5), new(5), 0, true},
		{"cost_zero_flex_zero", new(0), new(0), 0, true},
		{"cost_nil", nil, new(2), 0, false},
		{"flex_nil", new(2), nil, 0, false},
		{"both_nil", nil, nil, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			peer := NewPeer(nil, nil)
			peer.propagationStampCost = tc.cost
			peer.propagationStampCostFlexibility = tc.flex

			gotCost, gotOK := peer.MinAcceptedStampCost()
			if gotOK != tc.wantOK {
				t.Fatalf("MinAcceptedStampCost ok=%v want %v", gotOK, tc.wantOK)
			}
			if gotCost != tc.wantCost {
				t.Fatalf("MinAcceptedStampCost=%v want %v", gotCost, tc.wantCost)
			}
		})
	}
}

// TestMinAcceptedStampCostNilPeerDefensive covers Phase 17 task 5: a nil peer
// reports ok=false (cost 0) rather than panicking, so the future offer-prep
// branch can call it defensively.
func TestMinAcceptedStampCostNilPeerDefensive(t *testing.T) {
	t.Parallel()
	var peer *Peer
	gotCost, gotOK := peer.MinAcceptedStampCost()
	if gotOK {
		t.Fatalf("MinAcceptedStampCost on nil peer ok=%v want false", gotOK)
	}
	if gotCost != 0 {
		t.Fatalf("MinAcceptedStampCost on nil peer=%v want 0", gotCost)
	}
}
