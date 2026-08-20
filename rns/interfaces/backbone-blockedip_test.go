// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"testing"
	"time"
)

// TestBlockedIPListReturnsAllFlappedIPs verifies the exported
// BlockedIPList property mirrors Python's blocked_ip_list
// (BackboneInterface.py:532-534, v1.4.0), returning every registry key without
// purging and without filtering by the grace threshold.
func TestBlockedIPListReturnsAllFlappedIPs(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(true, 600, 3, 12*60*60, now)

	spawnedAt := time.Unix(9_999, 0)
	// Two IPs flap once each (flaps == 1 < grace 3): neither is blocked, but
	// both appear in the list because the list is raw registry keys.
	b.recordFlap("192.0.2.1", spawnedAt)
	b.recordFlap("192.0.2.2", spawnedAt)

	list := b.BlockedIPList()
	if len(list) != 2 {
		t.Fatalf("BlockedIPList len=%d want 2 (got %v)", len(list), list)
	}
	want := map[string]bool{"192.0.2.1": true, "192.0.2.2": true}
	for _, ip := range list {
		if !want[ip] {
			t.Fatalf("unexpected IP %q in list", ip)
		}
	}
}

// TestBlockedIPListEmptyWhenBlockingDisabled verifies that when
// block_fast_flapping is false, BlockedIPList returns nil/empty, mirroring
// Python's `if not self.block_fast_flapping: return []`
// (BackboneInterface.py:533-534).
func TestBlockedIPListEmptyWhenBlockingDisabled(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(false, 600, 3, 12*60*60, now)

	spawnedAt := time.Unix(9_999, 0)
	for range 10 {
		b.recordFlap("192.0.2.5", spawnedAt)
	}
	if got := b.BlockedIPList(); len(got) != 0 {
		t.Fatalf("BlockedIPList=%v want empty when blocking disabled", got)
	}
}

// TestBlockedIPCountCountsOnlyOverGrace verifies the exported
// BlockedIPCount property mirrors Python's blocked_ip_count
// (BackboneInterface.py:537-560, v1.3.9), counting only IPs whose flap count
// exceeds the grace threshold (flaps > grace), while unblocked flapped IPs do
// not contribute.
func TestBlockedIPCountCountsOnlyOverGrace(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(true, 600, 3, 12*60*60, now)

	spawnedAt := time.Unix(9_999, 0)
	// IP A: 2 flaps (< grace 3) — not blocked, not counted.
	// IP B: 4 flaps (> grace 3) — blocked, counted.
	// IP C: 5 flaps (> grace 3) — blocked, counted.
	for range 2 {
		b.recordFlap("192.0.2.10", spawnedAt)
	}
	for range 4 {
		b.recordFlap("192.0.2.11", spawnedAt)
	}
	for range 5 {
		b.recordFlap("192.0.2.12", spawnedAt)
	}

	if got, want := b.BlockedIPCount(), 2; got != want {
		t.Fatalf("BlockedIPCount=%d want %d", got, want)
	}
}

// TestBlockedIPCountPurgesExpired verifies that BlockedIPCount purges
// entries whose last flap is older than the expiry window before counting, so
// an expired blocked IP no longer contributes (BackboneInterface.py:548-557).
func TestBlockedIPCountPurgesExpired(t *testing.T) {
	t.Parallel()
	clock := int64(0)
	now := func() time.Time { return time.Unix(clock, 0) }
	b := newFastFlapBackbone(true, 600, 3, 100, now)

	spawnedAt := time.Unix(0, 0)
	for range 5 {
		b.recordFlap("192.0.2.20", spawnedAt) // blocked at t=0
	}
	if got := b.BlockedIPCount(); got != 1 {
		t.Fatalf("BlockedIPCount before expiry=%d want 1", got)
	}

	clock = 101 // past the 100s expiry window
	if got := b.BlockedIPCount(); got != 0 {
		t.Fatalf("BlockedIPCount after expiry=%d want 0", got)
	}
}

// TestBlockedIPCountZeroWhenBlockingDisabled verifies that when
// block_fast_flapping is false, BlockedIPCount returns 0 regardless of flap
// history (BackboneInterface.py:538).
func TestBlockedIPCountZeroWhenBlockingDisabled(t *testing.T) {
	t.Parallel()
	now := fastFlapNowFn(10_000)
	b := newFastFlapBackbone(false, 600, 3, 12*60*60, now)

	spawnedAt := time.Unix(9_999, 0)
	for range 10 {
		b.recordFlap("192.0.2.30", spawnedAt)
	}
	if got := b.BlockedIPCount(); got != 0 {
		t.Fatalf("BlockedIPCount=%d want 0 when blocking disabled", got)
	}
}
