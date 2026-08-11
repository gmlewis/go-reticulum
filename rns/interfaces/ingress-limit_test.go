// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"testing"
	"time"
)

// freqSamples builds an ia-freq deque with n samples spread back from now
// over span seconds, yielding a known incoming-announce frequency.
func freqSamples(now time.Time, n int, span float64) []time.Time {
	if n <= 0 {
		return nil
	}
	out := make([]time.Time, n)
	step := span / float64(n)
	for i := range n {
		// oldest first; last sample is ~now.
		out[i] = now.Add(-time.Duration((span - float64(i)*step) * float64(time.Second)))
	}
	return out
}

// TestShouldIngressLimit verifies the announce-burst state machine
// (Interface.py:152-172): ingress_control off ⇒ false; below threshold ⇒
// false; above threshold ⇒ activate + return true (setting icHeldRelease);
// while active always returns true, and deactivates only when freq drops,
// the hold window has elapsed, and the deque has >= IC_DEQUE_MIN_SAMPLE.
func TestShouldIngressLimit(t *testing.T) {
	t.Parallel()
	now := time.Unix(2_000_000, 0)

	// ingress_control disabled ⇒ never limit.
	t.Run("ingress_control off returns false", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("off", ModeFull, 62500)
		bi.SetIngressControl(false)
		bi.iaFreqDeque = freqSamples(now, 4, 0.5) // high freq, but disabled
		if bi.shouldIngressLimitAt(now) {
			t.Fatal("shouldIngressLimitAt = true with ingress_control off")
		}
	})

	// Fresh interface (age < ic_new_time) uses ic_burst_freq_new = 3 as the
	// threshold. 4 samples over 1s ⇒ 4 Hz > 3 ⇒ activate.
	t.Run("freq above new threshold activates", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("act", ModeFull, 62500)
		bi.iaFreqDeque = freqSamples(now, 4, 1.0) // hz = 4 > 3
		if !bi.shouldIngressLimitAt(now) {
			t.Fatal("expected activation (freq > threshold)")
		}
		if !bi.icBurstActive {
			t.Fatal("icBurstActive not set after activation")
		}
		if !bi.icBurstActivated.Equal(now) {
			t.Errorf("icBurstActivated = %v, want %v", bi.icBurstActivated, now)
		}
		wantRelease := now.Add(time.Duration(bi.icBurstPenalty) * time.Second)
		if !bi.icHeldRelease.Equal(wantRelease) {
			t.Errorf("icHeldRelease = %v, want %v (now+penalty)", bi.icHeldRelease, wantRelease)
		}
	})

	// 3 samples over 3s ⇒ 1 Hz < 3 ⇒ no activation.
	t.Run("freq below threshold does not activate", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("low", ModeFull, 62500)
		bi.iaFreqDeque = freqSamples(now, 3, 3.0) // hz = 1 < 3
		if bi.shouldIngressLimitAt(now) {
			t.Fatal("expected no activation (freq <= threshold)")
		}
		if bi.icBurstActive {
			t.Fatal("icBurstActive should remain false")
		}
	})

	// Already active: returns true regardless, and does NOT deactivate while
	// still inside the burst-hold window.
	t.Run("active returns true within hold window", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("hold", ModeFull, 62500)
		bi.icBurstActive = true
		bi.icBurstActivated = now.Add(-2 * time.Second) // 2s ago; hold=15s
		bi.iaFreqDeque = freqSamples(now, 3, 3.0)       // hz=1 < 3
		if !bi.shouldIngressLimitAt(now) {
			t.Fatal("active interface must return true")
		}
		if !bi.icBurstActive {
			t.Fatal("must not deactivate within hold window")
		}
	})

	// Active + past hold window + freq low + deque >= 2 ⇒ deactivate (but
	// this call still returns true).
	t.Run("active deactivates past hold window when freq low", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("deact", ModeFull, 62500)
		bi.icBurstActive = true
		bi.icBurstActivated = now.Add(-20 * time.Second) // 20s ago; hold=15s
		bi.iaFreqDeque = freqSamples(now, 3, 3.0)        // hz=1 < 3, deque>=2
		if !bi.shouldIngressLimitAt(now) {
			t.Fatal("active interface must return true even while deactivating")
		}
		if bi.icBurstActive {
			t.Fatal("expected deactivation (icBurstActive now false)")
		}
	})

	// Active + past hold window + freq low + deque < 2 ⇒ no deactivate.
	t.Run("active stays active with deque below min sample", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("short", ModeFull, 62500)
		bi.icBurstActive = true
		bi.icBurstActivated = now.Add(-20 * time.Second)
		bi.iaFreqDeque = freqSamples(now, 1, 1.0) // deque len 1 < 2
		if !bi.shouldIngressLimitAt(now) {
			t.Fatal("active interface must return true")
		}
		if !bi.icBurstActive {
			t.Fatal("must not deactivate with deque < IC_DEQUE_MIN_SAMPLE")
		}
	})

	// Old interface (age >= ic_new_time) uses ic_burst_freq = 10 as the
	// threshold. 3 samples over 1s ⇒ 3 Hz < 10 ⇒ no activation.
	t.Run("old interface uses higher steady threshold", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("old", ModeFull, 62500)
		bi.created = now.Add(-3 * time.Hour)      // age 3h > 7200s
		bi.iaFreqDeque = freqSamples(now, 3, 1.0) // hz=3 < 10
		if bi.shouldIngressLimitAt(now) {
			t.Fatal("expected no activation with steady threshold 10 (freq=3)")
		}
	})
}

// TestHoldAnnounce verifies the held-announce stash (Interface.py:224-230):
// a new destination is held while under ic_max_held_announces; a repeat
// destination replaces the held copy; once the cap is reached further new
// destinations are dropped.
func TestHoldAnnounce(t *testing.T) {
	t.Parallel()
	t.Run("holds up to max then drops", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("hold", ModeFull, 62500)
		bi.SetICMaxHeldAnnounces(3)
		for i := range 4 {
			bi.HoldAnnounce([]byte{byte(i)}, nil, i, []byte{0, 0, 0, byte(i)})
		}
		if got := bi.HeldAnnounces(); got != 3 {
			t.Fatalf("HeldAnnounces = %d, want 3 (4th dropped at cap)", got)
		}
	})
	t.Run("repeat destination replaces held copy", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("rep", ModeFull, 62500)
		dh := []byte{1, 2, 3, 4}
		bi.HoldAnnounce([]byte{0xA}, nil, 2, dh)
		bi.HoldAnnounce([]byte{0xB}, nil, 5, dh) // same dest, replaces
		if bi.HeldAnnounces() != 1 {
			t.Fatalf("HeldAnnounces = %d, want 1 (replace not add)", bi.HeldAnnounces())
		}
	})
	t.Run("max-hop announce not held", func(t *testing.T) {
		t.Parallel()
		// Interface.py:225-226 (v1.4.0): an announce at or beyond
		// PATHFINDER_M-1 hops is never held — it has already traveled too
		// far to be worth re-broadcasting once released.
		bi := NewBaseInterface("guard", ModeFull, 62500)
		bi.HoldAnnounce([]byte{0xA}, nil, PathfinderM-1, []byte{1}) // hops = 127
		bi.HoldAnnounce([]byte{0xB}, nil, PathfinderM, []byte{2})   // hops = 128
		if got := bi.HeldAnnounces(); got != 0 {
			t.Fatalf("HeldAnnounces = %d, want 0 (max-hop announces must not be held)", got)
		}
		// A just-below-threshold announce is still eligible.
		bi.HoldAnnounce([]byte{0xC}, nil, PathfinderM-2, []byte{3}) // hops = 126
		if got := bi.HeldAnnounces(); got != 1 {
			t.Fatalf("HeldAnnounces = %d, want 1 (sub-threshold announce should be held)", got)
		}
	})
}

// TestProcessHeldAnnounces verifies the release path (Interface.py:232-255):
// nothing released when there are no held announces or the release time has
// not elapsed or the frequency is still above threshold; otherwise the
// fewest-hops held announce is returned, removed from the stash, and
// ic_held_release advances by ic_held_release_interval.
func TestProcessHeldAnnounces(t *testing.T) {
	t.Parallel()
	now := time.Unix(3_000_000, 0)

	t.Run("empty held returns nothing", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("e", ModeFull, 62500)
		if _, _, ok := bi.processHeldAnnouncesAt(now); ok {
			t.Fatal("expected ok=false with no held announces")
		}
	})

	t.Run("before held release time returns nothing", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("early", ModeFull, 62500)
		bi.icHeldRelease = now.Add(10 * time.Second) // not yet
		bi.HoldAnnounce([]byte{0xA}, nil, 1, []byte{1})
		if _, _, ok := bi.processHeldAnnouncesAt(now); ok {
			t.Fatal("expected ok=false before icHeldRelease")
		}
	})

	t.Run("freq above threshold returns nothing", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("busy", ModeFull, 62500)
		bi.icHeldRelease = now.Add(-1 * time.Second) // elapsed
		bi.iaFreqDeque = freqSamples(now, 4, 0.5)    // hz=8 > 3
		bi.HoldAnnounce([]byte{0xA}, nil, 1, []byte{1})
		if _, _, ok := bi.processHeldAnnouncesAt(now); ok {
			t.Fatal("expected ok=false while freq above threshold")
		}
	})

	t.Run("releases fewest hops and advances release time", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("rel", ModeFull, 62500)
		bi.icHeldRelease = now.Add(-1 * time.Second) // elapsed
		bi.iaFreqDeque = freqSamples(now, 3, 6.0)    // hz=0.5 < 3
		// Hold three announces with differing hops.
		bi.HoldAnnounce([]byte{0xA}, nil, 3, []byte{1})
		bi.HoldAnnounce([]byte{0xB}, nil, 1, []byte{2}) // fewest hops
		bi.HoldAnnounce([]byte{0xC}, nil, 2, []byte{3})
		raw, _, ok := bi.processHeldAnnouncesAt(now)
		if !ok {
			t.Fatal("expected ok=true (release)")
		}
		if len(raw) != 1 || raw[0] != 0xB {
			t.Errorf("released raw = %x, want 0xB (fewest hops)", raw)
		}
		if bi.HeldAnnounces() != 2 {
			t.Errorf("HeldAnnounces after release = %d, want 2", bi.HeldAnnounces())
		}
		wantRelease := now.Add(time.Duration(bi.icHeldReleaseInterval) * time.Second)
		if !bi.icHeldRelease.Equal(wantRelease) {
			t.Errorf("icHeldRelease = %v, want %v (now+interval)", bi.icHeldRelease, wantRelease)
		}
	})
}
