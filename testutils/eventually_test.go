// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestPollUntilTrueImmediately(t *testing.T) {
	t.Parallel()

	start := time.Now()
	if !PollUntil(50*time.Millisecond, func() bool { return true }) {
		t.Fatal("PollUntil returned false for an immediately true condition")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("PollUntil took %v for an immediately true condition", elapsed)
	}
}

func TestPollUntilWaitsForCondition(t *testing.T) {
	t.Parallel()

	var ready atomic.Bool
	go func() {
		for i := 0; i < 20 && !ready.Load(); i++ {
			time.Sleep(10 * time.Millisecond)
		}
		ready.Store(true)
	}()
	if !PollUntil(2*time.Second, ready.Load) {
		t.Fatal("PollUntil never saw the condition become true")
	}
}

func TestPollUntilTimesOut(t *testing.T) {
	t.Parallel()

	if PollUntil(20*time.Millisecond, func() bool { return false }) {
		t.Fatal("PollUntil returned true for a condition that never held")
	}
}

func TestEventuallyDoesNotFailTheTest(t *testing.T) {
	t.Parallel()

	if Eventually(t, 20*time.Millisecond, func() bool { return false }) {
		t.Fatal("Eventually returned true for a never-true condition")
	}
}

func TestEventuallyFatalReportsFailure(t *testing.T) {
	t.Parallel()

	t.Run("failure is reported", func(t *testing.T) {
		fake := &fakeTB{}
		EventuallyFatal(fake, 10*time.Millisecond, func() bool { return false },
			"expected %v to happen", "thing")
		if len(fake.errors) != 1 {
			t.Fatalf("EventuallyFatal reported %d errors, want 1: %v", len(fake.errors), fake.errors)
		}
		if got := fake.errors[0]; got != "expected thing to happen" {
			t.Fatalf("EventuallyFatal error = %q, want %q", got, "expected thing to happen")
		}
	})

	t.Run("success is silent", func(t *testing.T) {
		fake := &fakeTB{}
		if !EventuallyFatal(fake, 10*time.Millisecond, func() bool { return true }, "nope") {
			t.Fatal("EventuallyFatal returned false for a true condition")
		}
		if len(fake.errors) != 0 {
			t.Fatalf("EventuallyFatal reported errors on success: %v", fake.errors)
		}
	})
}

func TestPollIntervalFor(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		timeout time.Duration
		want    time.Duration
	}{
		{50 * time.Millisecond, 5 * time.Millisecond},
		{200 * time.Millisecond, 5 * time.Millisecond},
		{5 * time.Second, 125 * time.Millisecond},
		{40 * time.Second, time.Second},
	} {
		if got := pollIntervalFor(tc.timeout); got != tc.want {
			t.Fatalf("pollIntervalFor(%v) = %v, want %v", tc.timeout, got, tc.want)
		}
	}
}
