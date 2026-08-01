// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "testing"

// TestResourceWatchdogJobIsNoOp pins Phase C.3: Resource.WatchdogJob is a
// no-op placeholder. Python's Resource.__watchdog_job (Resource.py:564-671)
// drives resource-transfer loss recovery (advertisement resend/cancel,
// receiver part-request retry with window reduction, sender timeout,
// AWAITING_PROOF cache query/cancel). The Go port does not yet implement this;
// it relies on the per-part RequestNext sliding window, which completes
// lossless transfers but does not recover from lost parts. This is an
// intentionally-deferred parity gap (see the WatchdogJob doc comment), pinned
// here per the TODO Phase C definition-of-done option. When the watchdog is
// ported, replace this test with golden tests of the timeout/cancel/retry
// state transitions captured from Python.
func TestResourceWatchdogJobIsNoOp(t *testing.T) {
	t.Parallel()

	// Across every state the Python watchdog acts on, the Go no-op must not
	// change the observable status or panic.
	for _, status := range []int{
		ResourceStatusAdvertised,
		ResourceStatusTransferring,
		ResourceStatusAwaitingProof,
		ResourceStatusRejected,
	} {
		r := &Resource{status: status}
		r.WatchdogJob()
		if r.status != status {
			t.Fatalf("WatchdogJob changed status %v -> %v (expected no-op)", status, r.status)
		}
	}

	// A nil resource must be safe to call.
	var nilRes *Resource
	nilRes.WatchdogJob()
}
