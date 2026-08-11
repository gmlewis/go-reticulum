// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"math"
	"testing"
	"time"
)

// TestIncomingAnnounceFrequency verifies the span/decay formula for the
// incoming-announce frequency (RNS/Interfaces/Interface.py:277-286, v1.2.5):
//
//	n = len(deque); if not n > IC_DEQUE_MIN_SAMPLE(2): return 0
//	oldest = deque[0]; span = now - oldest
//	if span > AR_FREQ_DECAY(10): popleft
//	if span <= 0: return 0
//	hz = n / span
//
// The returned hz uses the pre-pop n and the pre-pop span; the popleft is a
// side effect that ages the deque for the next call.
func TestIncomingAnnounceFrequency(t *testing.T) {
	t.Parallel()
	base := time.Unix(1_000_000, 0)

	tests := []struct {
		name    string
		samples []time.Time // appended left-to-right (oldest first)
		now     time.Time
		want    float64
	}{
		{"empty deque", nil, base, 0},
		{"one sample below min", []time.Time{base}, base, 0},
		{"two samples below min (>2 required)", []time.Time{base, base.Add(1 * time.Second)}, base.Add(1 * time.Second), 0},
		{
			"three samples span 4s",
			[]time.Time{base, base.Add(2 * time.Second), base.Add(4 * time.Second)},
			base.Add(4 * time.Second),
			3.0 / 4.0,
		},
		{
			"three samples span 6s",
			[]time.Time{base.Add(1 * time.Second), base.Add(3 * time.Second), base.Add(7 * time.Second)},
			base.Add(7 * time.Second),
			3.0 / 6.0,
		},
		{
			"decay pop oldest beyond 10s uses pre-pop n and span",
			[]time.Time{base, base.Add(2 * time.Second), base.Add(4 * time.Second)},
			base.Add(12 * time.Second), // span = 12 > AR_FREQ_DECAY(10) -> popleft
			3.0 / 12.0,
		},
		{"zero span returns zero", []time.Time{base, base, base}, base, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bi := NewBaseInterface("freq", ModeFull, 62500)
			bi.iaFreqDeque = append([]time.Time{}, tc.samples...)
			got := bi.incomingAnnounceFrequencyAt(tc.now)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("incomingAnnounceFrequencyAt = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIncomingAnnounceFrequencyDecayPop verifies the popleft side effect: a
// call whose span exceeds AR_FREQ_DECAY drops the oldest sample, so the next
// call sees a deque one entry shorter.
func TestIncomingAnnounceFrequencyDecayPop(t *testing.T) {
	t.Parallel()
	base := time.Unix(1_000_000, 0)
	bi := NewBaseInterface("freq", ModeFull, 62500)
	bi.iaFreqDeque = []time.Time{base, base.Add(2 * time.Second), base.Add(4 * time.Second)}

	// span = 12 > 10 -> popleft; returns pre-pop 3/12.
	got := bi.incomingAnnounceFrequencyAt(base.Add(12 * time.Second))
	if math.Abs(got-3.0/12.0) > 1e-9 {
		t.Fatalf("first call = %v, want %v", got, 3.0/12.0)
	}
	if len(bi.iaFreqDeque) != 2 {
		t.Fatalf("deque len after decay pop = %d, want 2", len(bi.iaFreqDeque))
	}
	// n=2 is not > IC_DEQUE_MIN_SAMPLE(2) -> 0.
	if got2 := bi.incomingAnnounceFrequencyAt(base.Add(12 * time.Second)); got2 != 0 {
		t.Fatalf("second call = %v, want 0 (n=2 not > 2)", got2)
	}
}

// TestOutgoingAnnounceFrequency verifies the outgoing-announce formula
// (Interface.py:288-297), which differs from the incoming one only in its
// minimum gate: `if not len > 1: return 0` (needs 2+ samples, not 3).
func TestOutgoingAnnounceFrequency(t *testing.T) {
	t.Parallel()
	base := time.Unix(1_000_000, 0)

	tests := []struct {
		name    string
		samples []time.Time
		now     time.Time
		want    float64
	}{
		{"empty deque", nil, base, 0},
		{"one sample below min (>1 required)", []time.Time{base}, base, 0},
		{"two samples span 5s", []time.Time{base, base.Add(5 * time.Second)}, base.Add(5 * time.Second), 2.0 / 5.0},
		{"three samples span 10s exactly no pop", []time.Time{base, base.Add(5 * time.Second), base.Add(10 * time.Second)}, base.Add(10 * time.Second), 3.0 / 10.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bi := NewBaseInterface("freq", ModeFull, 62500)
			bi.oaFreqDeque = append([]time.Time{}, tc.samples...)
			got := bi.outgoingAnnounceFrequencyAt(tc.now)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("outgoingAnnounceFrequencyAt = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestReceivedSentAnnouncePopulatesAndPropagates verifies that
// ReceivedAnnounce/SentAnnounce append a sample to the interface's own
// deque and, when a parent interface is set, propagate the sample to the
// parent's deque (Interface.py:257-265). The deque is capped at
// IAFreqSamples/OAFreqSamples.
func TestReceivedSentAnnouncePopulatesAndPropagates(t *testing.T) {
	t.Parallel()
	base := time.Unix(1_000_000, 0)

	parent := NewBaseInterface("parent", ModeFull, 62500)
	child := NewBaseInterface("child", ModeFull, 62500)
	child.parentInterface = parent

	child.receivedAnnounceAt(base.Add(1*time.Second), false)
	child.receivedAnnounceAt(base.Add(2*time.Second), false)
	child.sentAnnounceAt(base.Add(3*time.Second), false)

	if len(child.iaFreqDeque) != 2 {
		t.Errorf("child iaFreqDeque len = %d, want 2", len(child.iaFreqDeque))
	}
	if len(child.oaFreqDeque) != 1 {
		t.Errorf("child oaFreqDeque len = %d, want 1", len(child.oaFreqDeque))
	}
	if len(parent.iaFreqDeque) != 2 {
		t.Errorf("parent iaFreqDeque len = %d, want 2 (propagated)", len(parent.iaFreqDeque))
	}
	if len(parent.oaFreqDeque) != 1 {
		t.Errorf("parent oaFreqDeque len = %d, want 1 (propagated)", len(parent.oaFreqDeque))
	}
	// Parent must not re-propagate to a grandparent it does not have.
	if parent.parentInterface != nil {
		t.Fatal("parent has a parentInterface set; propagation should stop at the parent")
	}
}

// TestAnnounceFreqDequeCap verifies the sample deques are capped at
// IAFreqSamples/OAFreqSamples (Python deque(maxlen=48)).
func TestAnnounceFreqDequeCap(t *testing.T) {
	t.Parallel()
	base := time.Unix(1_000_000, 0)
	bi := NewBaseInterface("cap", ModeFull, 62500)
	for i := range IAFreqSamples + 10 {
		bi.receivedAnnounceAt(base.Add(time.Duration(i)*time.Millisecond), false)
	}
	if len(bi.iaFreqDeque) != IAFreqSamples {
		t.Errorf("iaFreqDeque len = %d, want capped at %d", len(bi.iaFreqDeque), IAFreqSamples)
	}
	for i := range OAFreqSamples + 10 {
		bi.sentAnnounceAt(base.Add(time.Duration(i)*time.Millisecond), false)
	}
	if len(bi.oaFreqDeque) != OAFreqSamples {
		t.Errorf("oaFreqDeque len = %d, want capped at %d", len(bi.oaFreqDeque), OAFreqSamples)
	}
}
