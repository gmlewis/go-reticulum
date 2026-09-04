// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"testing"
	"time"
)

// pollIntervalFor returns the poll interval for a timeout: a 5ms floor with
// roughly 40 samples per timeout, so short deadlines still poll promptly and
// long deadlines do not busy-loop.
func pollIntervalFor(timeout time.Duration) time.Duration {
	if p := timeout / 40; p > 5*time.Millisecond {
		return p
	}
	return 5 * time.Millisecond
}

// PollUntil polls cond every few milliseconds until it returns true or
// timeout elapses, and returns the final cond() value. It takes no
// *testing.T, so it can back package-local wrappers whose signatures predate
// this helper. Safe inside a synctest bubble (the poll sleep is virtualized)
// and from any goroutine.
func PollUntil(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if !time.Now().Before(deadline) {
			return cond()
		}
		time.Sleep(pollIntervalFor(timeout))
	}
}

// Eventually polls cond until it returns true or timeout elapses, reporting
// the final cond() value without failing the test (the caller decides). It
// never calls t.Fatal, so it is safe from any goroutine.
func Eventually(t testing.TB, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	return PollUntil(timeout, cond)
}

// EventuallyFatal is Eventually, plus t.Errorf(format, args...) when the
// condition never held within the timeout. Errorf (not Fatalf) is deliberate:
// t.Fatalf is forbidden from non-test goroutines, and a FailNow from inside a
// synctest bubble helper would abort the bubble at a confusing point.
func EventuallyFatal(t testing.TB, timeout time.Duration, cond func() bool, format string, args ...any) bool {
	t.Helper()
	if PollUntil(timeout, cond) {
		return true
	}
	t.Errorf(format, args...)
	return false
}
