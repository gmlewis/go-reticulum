// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package entropy

import (
	"errors"
	"fmt"
	"math"
	"testing"
)

func TestRepetitionCutoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		minEntropy float64
		alpha      float64
		want       int
		wantErr    bool
	}{
		{name: "byte samples at 2^-30", minEntropy: 8, alpha: DefaultAlpha, want: 5},
		{name: "byte samples at 2^-20", minEntropy: 8, alpha: 1.0 / (1 << 20), want: 4},
		{name: "nibble samples at 2^-20", minEntropy: 4, alpha: 1.0 / (1 << 20), want: 6},
		{name: "bit samples at 2^-30", minEntropy: 1, alpha: DefaultAlpha, want: 31},
		{name: "zero min entropy", minEntropy: 0, alpha: DefaultAlpha, wantErr: true},
		{name: "negative min entropy", minEntropy: -1, alpha: DefaultAlpha, wantErr: true},
		{name: "alpha of one", minEntropy: 8, alpha: 1, wantErr: true},
		{name: "alpha of zero", minEntropy: 8, alpha: 0, wantErr: true},
		{name: "alpha above one", minEntropy: 8, alpha: 2, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := RepetitionCutoff(tt.minEntropy, tt.alpha)
			if tt.wantErr {
				if !errors.Is(err, ErrBadParameter) {
					t.Fatalf("RepetitionCutoff(%v, %v) error = %v, want ErrBadParameter", tt.minEntropy, tt.alpha, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RepetitionCutoff(%v, %v) = %v", tt.minEntropy, tt.alpha, err)
			}
			if got != tt.want {
				t.Errorf("RepetitionCutoff(%v, %v) = %v, want %v", tt.minEntropy, tt.alpha, got, tt.want)
			}
		})
	}
}

// TestBinomialPMF pins the probability helper the cutoffs are derived from.
// The small cases are exact values a reader can check by hand, and the last
// case checks that the whole distribution over a real window still sums to one,
// which is what a log-space implementation gets wrong.
func TestBinomialPMF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		n    int
		k    int
		p    float64
		want float64
	}{
		{name: "two of four coin flips", n: 4, k: 2, p: 0.5, want: 0.375},
		{name: "no hits in ten draws", n: 10, k: 0, p: 0.25, want: 0.056313514709472656},
		{name: "one hit in one draw", n: 1, k: 1, p: 1.0 / 256, want: 1.0 / 256},
		{name: "out of range", n: 4, k: 5, p: 0.5, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := binomialPMF(tt.n, tt.k, tt.p); math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("binomialPMF(%v, %v, %v) = %v, want %v", tt.n, tt.k, tt.p, got, tt.want)
			}
		})
	}

	total := 0.0
	for k := 0; k <= DefaultWindow; k++ {
		total += binomialPMF(DefaultWindow, k, 1.0/256)
	}
	if math.Abs(total-1) > 1e-9 {
		t.Errorf("binomialPMF over a full window sums to %v, want 1", total)
	}
}

// TestAdaptiveProportionCutoffDefinition checks each cutoff against the defining
// property: the cutoff is the smallest count whose upper tail is already within
// alpha, and the next count down is not.
func TestAdaptiveProportionCutoffDefinition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		window     int
		minEntropy float64
		alpha      float64
	}{
		{name: "default parameters", window: DefaultWindow, minEntropy: DefaultMinEntropyBitsPerSample, alpha: DefaultAlpha},
		{name: "one bit per sample", window: 1024, minEntropy: 1, alpha: DefaultAlpha},
		{name: "narrow window", window: 64, minEntropy: 8, alpha: DefaultAlpha},
		{name: "lenient alpha", window: 1024, minEntropy: 8, alpha: 1.0 / (1 << 10)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := AdaptiveProportionCutoff(tt.window, tt.minEntropy, tt.alpha)
			if err != nil {
				t.Fatalf("AdaptiveProportionCutoff(%v, %v, %v) = %v", tt.window, tt.minEntropy, tt.alpha, err)
			}

			p := 1 / math.Exp2(tt.minEntropy)
			mean := float64(tt.window) * p
			if float64(got) <= mean {
				t.Errorf("cutoff %v is not above the mean %v; the test would fire on a healthy source", got, mean)
			}
			if tail := binomialTail(tt.window, got, p); tail > tt.alpha {
				t.Errorf("P(X >= %v) = %v, want <= alpha (%v)", got, tail, tt.alpha)
			}
			if tail := binomialTail(tt.window, got-1, p); tail <= tt.alpha {
				t.Errorf("P(X >= %v) = %v, want > alpha (%v); the cutoff is later than it needs to be",
					got-1, tail, tt.alpha)
			}
		})
	}
}

// binomialTail sums P(X >= k) for X ~ Binomial(n, p) from the top down, so the
// negligible terms underflow instead of the large ones.
func binomialTail(n, k int, p float64) float64 {
	total := 0.0
	for j := n; j >= k; j-- {
		total += binomialPMF(n, j, p)
	}
	return total
}

func TestAdaptiveProportionCutoffRejectsBadParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		window     int
		minEntropy float64
		alpha      float64
	}{
		{name: "window of one", window: 1, minEntropy: 8, alpha: DefaultAlpha},
		{name: "zero min entropy", window: 1024, minEntropy: 0, alpha: DefaultAlpha},
		{name: "alpha of one", window: 1024, minEntropy: 8, alpha: 1},
		{name: "window too narrow for the cutoff", window: 2, minEntropy: 8, alpha: DefaultAlpha},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := AdaptiveProportionCutoff(tt.window, tt.minEntropy, tt.alpha); !errors.Is(err, ErrBadParameter) {
				t.Errorf("AdaptiveProportionCutoff(%v, %v, %v) error = %v, want ErrBadParameter",
					tt.window, tt.minEntropy, tt.alpha, err)
			}
		})
	}
}

// TestHealthTripsOnStuckSource is the failure the gate exists for: a register
// that has stopped moving. The repetition count test must reject it, and must
// reject it at the derived cutoff rather than after some arbitrary delay.
func TestHealthTripsOnStuckSource(t *testing.T) {
	t.Parallel()

	for _, stuck := range []byte{0x00, 0xFF, 0x5A} {
		t.Run(fmt.Sprintf("%#x", stuck), func(t *testing.T) {
			t.Parallel()

			var h Health
			repetitionCutoff, _, err := h.Cutoffs()
			if err != nil {
				t.Fatalf("Cutoffs() = %v", err)
			}

			var seen int
			for range 256 {
				seen++
				err := h.Observe(stuck)
				if err == nil {
					continue
				}
				if !errors.Is(err, ErrHealthTestFailed) {
					t.Fatalf("Observe(%#x) error = %v, want ErrHealthTestFailed", stuck, err)
				}
				if seen != repetitionCutoff {
					t.Errorf("stuck source rejected after %v samples, want %v", seen, repetitionCutoff)
				}
				return
			}
			t.Errorf("a source stuck at %#x was never rejected", stuck)
		})
	}
}

// TestHealthTripsOnTwoValuedSource covers the other shape of dead silicon: a
// generator alternating between two values. The repetition count test cannot
// see it (no two samples in a row are equal), so the adaptive proportion test
// has to.
func TestHealthTripsOnTwoValuedSource(t *testing.T) {
	t.Parallel()

	var h Health
	var err error
	for i := range 4 * DefaultWindow {
		sample := byte(0x00)
		if i%2 == 1 {
			sample = 0x0F
		}
		if err = h.Observe(sample); err != nil {
			break
		}
	}
	if !errors.Is(err, ErrHealthTestFailed) {
		t.Fatalf("two-valued source error = %v, want ErrHealthTestFailed", err)
	}
}

// TestHealthAcceptsBalancedSource checks the other direction: a source that is
// merely deterministic, but perfectly balanced, must not be rejected — the
// tests are there to catch dead silicon, not to demand secrecy they cannot
// measure.
func TestHealthAcceptsBalancedSource(t *testing.T) {
	t.Parallel()

	var h Health
	for i := range 4 * DefaultWindow {
		if err := h.Observe(byte(i)); err != nil {
			t.Fatalf("Observe(byte(%v)) = %v, want nil", i, err)
		}
	}
}

func TestHealthResetClearsHistory(t *testing.T) {
	t.Parallel()

	var h Health
	for range 256 {
		if err := h.Observe(0xAA); err == nil {
			continue
		}
		break
	}

	h.Reset()
	for i := range 4 * DefaultWindow {
		if err := h.Observe(byte(i)); err != nil {
			t.Fatalf("Observe after Reset = %v, want nil", err)
		}
	}
}

func TestHealthRejectsImpossibleParameters(t *testing.T) {
	t.Parallel()

	h := &Health{Alpha: 2}
	if err := h.Observe(0x01); !errors.Is(err, ErrBadParameter) {
		t.Errorf("Observe with alpha 2 error = %v, want ErrBadParameter", err)
	}
}

func TestHealthCutoffsUseDefaults(t *testing.T) {
	t.Parallel()

	var h Health
	repetition, window, err := h.Cutoffs()
	if err != nil {
		t.Fatalf("Cutoffs() = %v", err)
	}
	wantRepetition, err := RepetitionCutoff(DefaultMinEntropyBitsPerSample, DefaultAlpha)
	if err != nil {
		t.Fatalf("RepetitionCutoff() = %v", err)
	}
	wantWindow, err := AdaptiveProportionCutoff(DefaultWindow, DefaultMinEntropyBitsPerSample, DefaultAlpha)
	if err != nil {
		t.Fatalf("AdaptiveProportionCutoff() = %v", err)
	}
	if repetition != wantRepetition || window != wantWindow {
		t.Errorf("zero-value cutoffs = (%v, %v), want (%v, %v)", repetition, window, wantRepetition, wantWindow)
	}
}
