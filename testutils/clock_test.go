// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"sync"
	"testing"
	"time"
)

func TestStepClockStartsAndAdvances(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	clock := NewStepClock(start)
	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("NewStepClock Now() = %v, want %v", got, start)
	}

	next := clock.Advance(time.Second)
	if got := clock.Now(); !got.Equal(start.Add(time.Second)) || !next.Equal(clock.Now()) {
		t.Fatalf("Advance(1s) did not move the clock: now=%v advance=%v", clock.Now(), next)
	}

	// Two advances compose.
	clock.Advance(500 * time.Millisecond)
	if got := clock.Now(); !got.Equal(start.Add(1500 * time.Millisecond)) {
		t.Fatalf("second Advance left clock at %v", got)
	}
}

func TestStepClockIgnoresNegativeAdvance(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	clock := NewStepClock(start)
	clock.Advance(-time.Hour)
	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("negative Advance moved the clock to %v", got)
	}
}

func TestStepClockIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	clock := NewStepClock(time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				clock.Advance(time.Millisecond)
				_ = clock.Now()
			}
		})
	}
	wg.Wait()
	if got := clock.Now().Sub(clock.Now()); got != 0 {
		t.Fatalf("clock not stable under concurrency: %v", got)
	}
}
