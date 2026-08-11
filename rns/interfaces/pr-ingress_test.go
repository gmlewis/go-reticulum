// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"testing"
	"time"
)

// TestIncomingPrFrequency verifies the incoming path-request frequency formula
// (Interface.py:299-308): n/span over the ip_freq_deque with the PR_FREQ_DECAY
// (10s) popleft window and the IC_DEQUE_MIN_SAMPLE (2) gate.
func TestIncomingPrFrequency(t *testing.T) {
	t.Parallel()
	now := time.Unix(5_000_000, 0)

	// Empty / below-min-sample deques report zero.
	bi := NewBaseInterface("pr", ModeFull, 62500)
	if got := bi.incomingPrFrequencyAt(now); got != 0 {
		t.Fatalf("incomingPrFrequencyAt empty = %v, want 0", got)
	}

	// 4 samples over 1s ⇒ 4 Hz.
	bi.ipFreqDeque = freqSamples(now, 4, 1.0)
	if got, want := bi.incomingPrFrequencyAt(now), 4.0; got != want {
		t.Fatalf("incomingPrFrequencyAt = %v, want %v", got, want)
	}

	// Span beyond PR_FREQ_DECAY (10s) poplefts the oldest sample and reports
	// over the remaining samples.
	evalNow := now.Add(12 * time.Second)
	bi.ipFreqDeque = freqSamples(evalNow, 4, 12.0) // oldest ~12s before evalNow
	got := bi.incomingPrFrequencyAt(evalNow)
	if got <= 0 {
		t.Fatalf("incomingPrFrequencyAt after decay = %v, want > 0", got)
	}
	if len(bi.ipFreqDeque) != 3 {
		t.Fatalf("expected popleft to shrink deque to 3, got %d", len(bi.ipFreqDeque))
	}
}

// TestOutgoingPrFrequency verifies the outgoing path-request frequency formula
// (Interface.py:310-319): n/span over the op_freq_deque with the PR_FREQ_DECAY
// popleft window and the `len > 1` (2+ sample) gate.
func TestOutgoingPrFrequency(t *testing.T) {
	t.Parallel()
	now := time.Unix(5_000_000, 0)

	bi := NewBaseInterface("opr", ModeFull, 62500)
	bi.opFreqDeque = freqSamples(now, 1, 1.0) // len 1, fails len > 1 gate
	if got := bi.outgoingPrFrequencyAt(now); got != 0 {
		t.Fatalf("outgoingPrFrequencyAt len1 = %v, want 0", got)
	}

	bi.opFreqDeque = freqSamples(now, 5, 1.0) // 5 Hz
	if got, want := bi.outgoingPrFrequencyAt(now), 5.0; got != want {
		t.Fatalf("outgoingPrFrequencyAt = %v, want %v", got, want)
	}
}

// TestReceivedSentPathRequest verifies the PR frequency deques are populated by
// the received/sent path-request recorders and that a spawned peer propagates
// each event to its parent server interface (Interface.py:267-275), matching the
// announce-side from_spawned propagation.
func TestReceivedSentPathRequest(t *testing.T) {
	t.Parallel()
	now := time.Unix(6_000_000, 0)

	parent := NewBaseInterface("parent", ModeFull, 62500)
	child := NewBaseInterface("child", ModeFull, 62500)
	child.parentInterface = parent

	child.receivedPathRequestAt(now, false)
	child.sentPathRequestAt(now, false)

	if len(child.ipFreqDeque) != 1 || len(child.opFreqDeque) != 1 {
		t.Fatalf("child deques: ip=%d op=%d, want 1/1", len(child.ipFreqDeque), len(child.opFreqDeque))
	}
	if len(parent.ipFreqDeque) != 1 || len(parent.opFreqDeque) != 1 {
		t.Fatalf("parent deques not propagated: ip=%d op=%d, want 1/1", len(parent.ipFreqDeque), len(parent.opFreqDeque))
	}

	// A from_spawned propagation must not re-propagate to the grandparent.
	grandparent := NewBaseInterface("gp", ModeFull, 62500)
	parent.parentInterface = grandparent
	parent.receivedPathRequestAt(now, true)
	parent.sentPathRequestAt(now, true)
	if len(grandparent.ipFreqDeque) != 0 || len(grandparent.opFreqDeque) != 0 {
		t.Fatalf("grandparent re-propagated: ip=%d op=%d, want 0/0", len(grandparent.ipFreqDeque), len(grandparent.opFreqDeque))
	}
}

// TestShouldIngressLimitPr verifies the PR-burst state machine
// (Interface.py:174-190): ingress_control off ⇒ false; below threshold ⇒
// false; above threshold ⇒ activate + return true (setting
// ic_pr_burst_activated, but NO held-release penalty); while active always
// returns true and deactivates (without a deque-min-sample gate, unlike the
// announce burst) when freq drops and the hold window has elapsed.
func TestShouldIngressLimitPr(t *testing.T) {
	t.Parallel()
	now := time.Unix(7_000_000, 0)

	t.Run("ingress_control off returns false", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("off", ModeFull, 62500)
		bi.SetIngressControl(false)
		bi.ipFreqDeque = freqSamples(now, 4, 0.5)
		if bi.shouldIngressLimitPrAt(now) {
			t.Fatal("shouldIngressLimitPrAt = true with ingress_control off")
		}
	})

	t.Run("freq above new threshold activates", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("act", ModeFull, 62500)
		bi.ipFreqDeque = freqSamples(now, 4, 1.0) // 4 Hz > 3
		if !bi.shouldIngressLimitPrAt(now) {
			t.Fatal("expected activation (freq > threshold)")
		}
		if !bi.icPrBurstActive {
			t.Fatal("icPrBurstActive not set after activation")
		}
		if !bi.icPrBurstActivated.Equal(now) {
			t.Errorf("icPrBurstActivated = %v, want %v", bi.icPrBurstActivated, now)
		}
		// No held-release penalty on PR activation (unlike announce burst).
		if !bi.icHeldRelease.IsZero() {
			t.Errorf("icHeldRelease = %v, want zero (PR burst sets no penalty)", bi.icHeldRelease)
		}
	})

	t.Run("freq below threshold does not activate", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("low", ModeFull, 62500)
		bi.ipFreqDeque = freqSamples(now, 3, 3.0) // 1 Hz < 3
		if bi.shouldIngressLimitPrAt(now) {
			t.Fatal("expected no activation (freq <= threshold)")
		}
		if bi.icPrBurstActive {
			t.Fatal("icPrBurstActive should remain false")
		}
	})

	t.Run("active returns true within hold window", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("hold", ModeFull, 62500)
		bi.icPrBurstActive = true
		bi.icPrBurstActivated = now.Add(-2 * time.Second) // 2s ago; hold=15s
		bi.ipFreqDeque = freqSamples(now, 3, 3.0)         // 1 Hz < 3
		if !bi.shouldIngressLimitPrAt(now) {
			t.Fatal("active interface must return true")
		}
		if !bi.icPrBurstActive {
			t.Fatal("must not deactivate within hold window")
		}
	})

	t.Run("active deactivates past hold window when freq low", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("deact", ModeFull, 62500)
		bi.icPrBurstActive = true
		bi.icPrBurstActivated = now.Add(-20 * time.Second) // 20s ago; hold=15s
		bi.ipFreqDeque = freqSamples(now, 3, 3.0)          // 1 Hz < 3
		if !bi.shouldIngressLimitPrAt(now) {
			t.Fatal("active interface must return true even while deactivating")
		}
		if bi.icPrBurstActive {
			t.Fatal("expected deactivation (icPrBurstActive now false)")
		}
	})

	t.Run("active deactivates without deque-min-sample gate", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("short", ModeFull, 62500)
		bi.icPrBurstActive = true
		bi.icPrBurstActivated = now.Add(-20 * time.Second)
		// A single sample yields freq 0 (below threshold), and PR deactivation
		// has no IC_DEQUE_MIN_SAMPLE gate, so it still deactivates.
		bi.ipFreqDeque = freqSamples(now, 1, 1.0)
		if !bi.shouldIngressLimitPrAt(now) {
			t.Fatal("active interface must return true")
		}
		if bi.icPrBurstActive {
			t.Fatal("expected deactivation even with deque < IC_DEQUE_MIN_SAMPLE")
		}
	})

	t.Run("old interface uses higher steady threshold", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("old", ModeFull, 62500)
		bi.created = now.Add(-3 * time.Hour)      // age 3h > 7200s
		bi.ipFreqDeque = freqSamples(now, 4, 1.0) // 4 Hz < 8
		if bi.shouldIngressLimitPrAt(now) {
			t.Fatal("expected no activation with steady threshold 8 (freq=4)")
		}
	})
}

// TestShouldEgressLimitPr verifies the PR egress-limit gate (Interface.py:192-200):
// egress_control off ⇒ false; op_freq above ec_pr_freq(5) with at least
// IC_BURST_MIN_SAMPLES(6) samples ⇒ true; above the frequency threshold but
// with too few samples ⇒ false; at or below the threshold ⇒ false.
func TestShouldEgressLimitPr(t *testing.T) {
	t.Parallel()
	now := time.Unix(8_000_000, 0)

	t.Run("egress_control off returns false", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("off", ModeFull, 62500)
		bi.SetEgressControl(false)
		bi.opFreqDeque = freqSamples(now, 8, 1.0) // 8 Hz > 5, but disabled
		if bi.shouldEgressLimitPrAt(now) {
			t.Fatal("shouldEgressLimitPrAt = true with egress_control off")
		}
	})

	t.Run("above threshold with enough samples limits", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("limit", ModeFull, 62500)
		bi.SetEgressControl(true)
		bi.opFreqDeque = freqSamples(now, 7, 1.0) // 7 Hz > 5, deque 7 >= 6
		if !bi.shouldEgressLimitPrAt(now) {
			t.Fatal("expected egress limiting (freq > 5, deque >= 6)")
		}
	})

	t.Run("above threshold but too few samples does not limit", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("few", ModeFull, 62500)
		bi.SetEgressControl(true)
		bi.opFreqDeque = freqSamples(now, 5, 0.5) // 10 Hz > 5, but deque 5 < 6
		if bi.shouldEgressLimitPrAt(now) {
			t.Fatal("expected no limiting with deque < IC_BURST_MIN_SAMPLES")
		}
	})

	t.Run("at or below threshold does not limit", func(t *testing.T) {
		t.Parallel()
		bi := NewBaseInterface("low", ModeFull, 62500)
		bi.SetEgressControl(true)
		bi.opFreqDeque = freqSamples(now, 3, 3.0) // 1 Hz < 5
		if bi.shouldEgressLimitPrAt(now) {
			t.Fatal("expected no limiting (freq <= 5)")
		}
	})
}
