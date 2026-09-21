// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package entropy

import (
	"fmt"
	"math"
)

// Default parameters for the on-line health tests.
const (
	// DefaultAlpha is the false-positive probability allowed for each on-line
	// health test, matching NIST SP 800-90B's 2^-30 recommendation. A healthy
	// generator trips a test at this rate, which is why the gate reseeds from
	// the accumulated pool rather than discarding the device's identity when a
	// test fires on good hardware.
	DefaultAlpha = 1.0 / (1 << 30)

	// DefaultMinEntropyBitsPerSample is the min-entropy in bits the health
	// tests assume for each sample. Samples are bytes, and the assumption used
	// for the tests is the healthy case — a well-behaved byte generator holds
	// close to 8 bits — which is what makes the tests sensitive to bias.
	//
	// This is deliberately NOT the assumption used for crediting entropy. The
	// gate credits DefaultMinEntropyBitsPerByte per accepted byte, an order of
	// magnitude below this, so the two assumptions both err toward caution:
	// the tests are strict enough to notice a degraded source, and the credit
	// banks far less than the source appears to offer.
	DefaultMinEntropyBitsPerSample = 8.0

	// DefaultWindow is the adaptive proportion test window W, in samples.
	DefaultWindow = 1024
)

// Health runs the NIST SP 800-90B on-line health tests over a raw entropy
// source: the repetition count test and the adaptive proportion test. Between
// them they catch the failures hardware actually exhibits — a register stuck at
// a constant, a source that has become grossly biased, an unclocked peripheral
// answering with the same word forever.
//
// Samples are fed through Observe one at a time, before any byte of them can
// reach the entropy pool. The zero value is ready to use and applies the
// default parameters.
type Health struct {
	// Alpha is the false-positive probability allowed per test. Zero selects
	// DefaultAlpha; a value outside (0, 1) is rejected.
	Alpha float64

	// MinEntropyBitsPerSample is the assumed min-entropy per sample, in bits,
	// used only to derive the two cutoffs. Zero selects
	// DefaultMinEntropyBitsPerSample; a value at or below zero is rejected.
	MinEntropyBitsPerSample float64

	// Window is the adaptive proportion test window W, in samples, counted as
	// byte samples. Zero selects DefaultWindow; a value below 2 is rejected.
	Window int

	prepared         bool
	prepareErr       error
	repetitionCutoff int
	windowCutoff     int

	lastSample  byte
	runLength   int
	windowFirst byte
	windowCount int
	windowIndex int
}

// RepetitionCutoff returns the SP 800-90B repetition count cutoff for the given
// per-sample min-entropy and false-positive probability:
//
//	C = 1 + ceil(-log2(alpha) / H)
//
// H is the assumed min-entropy per sample, in bits. A source that produces C
// identical samples in a row has shown that it holds at most H*(C-1) bits where
// the assumption credited it H*C, and the test rejects the source at
// significance alpha.
func RepetitionCutoff(minEntropyBitsPerSample, alpha float64) (int, error) {
	if minEntropyBitsPerSample <= 0 || math.IsInf(minEntropyBitsPerSample, 0) || math.IsNaN(minEntropyBitsPerSample) {
		return 0, fmt.Errorf("%w: min-entropy per sample %v", ErrBadParameter, minEntropyBitsPerSample)
	}
	if alpha <= 0 || alpha >= 1 || math.IsNaN(alpha) {
		return 0, fmt.Errorf("%w: alpha %v", ErrBadParameter, alpha)
	}
	return 1 + int(math.Ceil(-math.Log2(alpha)/minEntropyBitsPerSample)), nil
}

// AdaptiveProportionCutoff returns the smallest count k for which the chance of
// seeing k or more samples equal to the first sample of a window is at most
// alpha, when the source is assumed to carry minEntropyBitsPerSample bits per
// sample and the window holds window samples:
//
//	k = min { k : P(X >= k) <= alpha },  X ~ Binomial(window, 2^-H)
//
// It returns ErrBadParameter if no such k falls inside the window, which means
// the parameters cannot detect anything the test is looking for.
func AdaptiveProportionCutoff(window int, minEntropyBitsPerSample, alpha float64) (int, error) {
	if window < 2 {
		return 0, fmt.Errorf("%w: window %v", ErrBadParameter, window)
	}
	if minEntropyBitsPerSample <= 0 || math.IsNaN(minEntropyBitsPerSample) {
		return 0, fmt.Errorf("%w: min-entropy per sample %v", ErrBadParameter, minEntropyBitsPerSample)
	}
	if alpha <= 0 || alpha >= 1 || math.IsNaN(alpha) {
		return 0, fmt.Errorf("%w: alpha %v", ErrBadParameter, alpha)
	}

	// The cutoff sits far out in the upper tail, so the probability is
	// accumulated from the largest counts downward. Terms that are already
	// negligible underflow to zero, which costs nothing: they are below the
	// significance the test is looking for.
	p := math.Exp2(-minEntropyBitsPerSample)
	cumulative := 0.0
	for k := window; k >= 1; k-- {
		cumulative += binomialPMF(window, k, p)
		if cumulative > alpha {
			if k+1 > window {
				return 0, fmt.Errorf("%w: window %v cannot bound a %v-bit source at alpha %v",
					ErrBadParameter, window, minEntropyBitsPerSample, alpha)
			}
			return k + 1, nil
		}
	}
	return 0, fmt.Errorf("%w: window %v cannot bound a %v-bit source at alpha %v",
		ErrBadParameter, window, minEntropyBitsPerSample, alpha)
}

// binomialPMF returns P(X == k) for X ~ Binomial(n, p), computed in log space so
// the trial count and the tail probabilities do not overflow float64.
func binomialPMF(n, k int, p float64) float64 {
	if k < 0 || k > n {
		return 0
	}
	logCoefficient, _ := math.Lgamma(float64(n) + 1)
	logK, _ := math.Lgamma(float64(k) + 1)
	logNK, _ := math.Lgamma(float64(n-k) + 1)
	logPMF := logCoefficient - logK - logNK
	if k > 0 {
		logPMF += float64(k) * math.Log(p)
	}
	if n-k > 0 {
		logPMF += float64(n-k) * math.Log1p(-p)
	}
	return math.Exp(logPMF)
}

// prepare fills in the defaults and derives the cutoffs, once. It is called
// from every entry point so the zero value behaves like a fully configured
// Health.
func (h *Health) prepare() {
	if h.prepared {
		return
	}
	h.prepared = true
	if h.Alpha == 0 {
		h.Alpha = DefaultAlpha
	}
	if h.MinEntropyBitsPerSample == 0 {
		h.MinEntropyBitsPerSample = DefaultMinEntropyBitsPerSample
	}
	if h.Window == 0 {
		h.Window = DefaultWindow
	}
	h.repetitionCutoff, h.prepareErr = RepetitionCutoff(h.MinEntropyBitsPerSample, h.Alpha)
	if h.prepareErr != nil {
		return
	}
	h.windowCutoff, h.prepareErr = AdaptiveProportionCutoff(h.Window, h.MinEntropyBitsPerSample, h.Alpha)
}

// Cutoffs reports the two derived cutoffs, deriving them on first use. It
// exists so a caller can log exactly what the tests will tolerate, and so a
// test can check the derivation against its definition.
func (h *Health) Cutoffs() (repetition, window int, err error) {
	h.prepare()
	return h.repetitionCutoff, h.windowCutoff, h.prepareErr
}

// Reset clears the test state so a new source, or a new collection run, starts
// with no history. The derived cutoffs are kept.
func (h *Health) Reset() {
	h.lastSample = 0
	h.runLength = 0
	h.windowFirst = 0
	h.windowCount = 0
	h.windowIndex = 0
}

// Observe feeds one raw sample through both on-line tests. It returns an error
// wrapping ErrHealthTestFailed as soon as either test rejects the source.
func (h *Health) Observe(sample byte) error {
	h.prepare()
	if h.prepareErr != nil {
		return h.prepareErr
	}

	if sample == h.lastSample {
		h.runLength++
	} else {
		h.lastSample = sample
		h.runLength = 1
	}
	if h.runLength >= h.repetitionCutoff {
		return fmt.Errorf("%w: %v identical samples in a row (cutoff %v)",
			ErrHealthTestFailed, h.runLength, h.repetitionCutoff)
	}

	if h.windowIndex == 0 {
		h.windowFirst = sample
		h.windowCount = 0
	}
	if sample == h.windowFirst {
		h.windowCount++
	}
	h.windowIndex++
	if h.windowIndex >= h.Window {
		h.windowIndex = 0
	}
	if h.windowCount >= h.windowCutoff {
		return fmt.Errorf("%w: %v of %v samples in one window equal %#x (cutoff %v)",
			ErrHealthTestFailed, h.windowCount, h.Window, h.windowFirst, h.windowCutoff)
	}
	return nil
}
