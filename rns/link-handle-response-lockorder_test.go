// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync"
	"testing"
	"time"
)

// TestLinkHandleResponseReleasesLinkLockBeforeReceiptLock is a regression guard
// for the AB-BA deadlock that once hung TestIntegrationReleaseRoundTrip.
//
// requestResourceConcluded (request.go) holds rr.mu and then acquires l.mu via
// Link.removePendingRequest. handleResponse must therefore NOT hold l.mu while
// acquiring rr.mu (via RequestReceipt.MaxResponseSize), or the two paths invert
// lock order and deadlock: handleResponse holds l.mu waiting on rr.mu while
// requestResourceConcluded holds rr.mu waiting on l.mu.
//
// The fix moved found.MaxResponseSize() to after l.mu.Unlock() in handleResponse.
// This test asserts that invariant directly and deterministically — no reliance
// on lucky scheduling:
//
//  1. A helper goroutine holds rr.mu, so handleResponse cannot acquire it and
//     must block there once it reaches MaxResponseSize.
//  2. handleResponse removes the receipt from pendingRequests under l.mu
//     before any rr.mu acquisition, so that removal proceeds regardless. We
//     observe it (pendingRequests becomes empty) to know handleResponse has
//     finished its l.mu critical section.
//  3. We require l.mu to be free at that point. With the fix, handleResponse
//     released l.mu and is blocked on rr.mu, so l.mu is free. If the fix is
//     reverted, handleResponse is blocked on rr.mu while still holding l.mu,
//     so l.mu stays locked, TryLock never succeeds, and the test fails.
//
// All l.mu acquisition in the test uses TryLock: a blocking Lock would itself
// deadlock against the regressed handleResponse that holds l.mu.
func TestLinkHandleResponseReleasesLinkLockBeforeReceiptLock(t *testing.T) {
	t.Parallel()

	requestID := []byte("lock-order-req")
	rr := &RequestReceipt{RequestID: requestID, Status: RequestDelivered}
	link := &Link{logger: NewLogger(), pendingRequests: []*RequestReceipt{rr}}
	link.status.Store(LinkActive)

	// Helper holds rr.mu for the whole test so handleResponse blocks on it.
	rrHeld := make(chan struct{})
	release := make(chan struct{})
	go func() {
		rr.mu.Lock()
		defer rr.mu.Unlock()
		close(rrHeld)
		<-release
	}()
	<-rrHeld

	done := make(chan struct{})
	go func() {
		defer close(done)
		// checkSize=false still calls MaxResponseSize unconditionally, so this
		// exercises the rr.mu acquisition that must happen outside l.mu.
		link.handleResponse(requestID, []byte("resp"), nil, 0, 0, false, false)
	}()

	deadline := time.Now().Add(3 * time.Second)

	// Poll l.mu with TryLock only — a blocking Lock would itself deadlock when
	// the regression holds l.mu. Success requires BOTH that we can acquire l.mu
	// (it is free) AND that handleResponse has already removed the pending
	// request (pendingRequests is empty). The list starts non-empty, so the
	// only way to observe empty-while-l.mu-is-free is for handleResponse to have
	// finished its l.mu critical section: it removes the entry under l.mu, then
	// (with the fix) releases l.mu before blocking on rr.mu.
	//
	// Fixed: handleResponse releases l.mu and blocks on rr.mu (held by the
	// helper), so TryLock succeeds and the list is empty -> pass.
	// Reverted: handleResponse blocks on rr.mu while still holding l.mu, so
	// TryLock never succeeds before the deadline -> fail.
	ok := false
	for time.Now().Before(deadline) {
		if !link.mu.TryLock() {
			time.Sleep(time.Millisecond)
			continue
		}
		n := len(link.pendingRequests)
		link.mu.Unlock()
		if n == 0 {
			ok = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	if !ok {
		t.Fatal("l.mu was held after handleResponse removed the pending request: " +
			"AB-BA lock-order inversion regressed — handleResponse must release " +
			"l.mu before acquiring rr.mu (move MaxResponseSize after l.mu.Unlock)")
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleResponse did not complete after the helper released rr.mu")
	}
}

// TestRequestResourceConcludedReadsStatusUnderResourceLock is a -race regression
// guard for the data race between requestResourceConcluded and Resource.Request
// (resource.go:994) found by the race detector in the same failure log.
//
// requestResourceConcluded is the Resource callback (spawned via
// `go r.callback(r)` from ValidateProof/Request). It reads the resource's
// status to decide Complete vs Failed. The resource's status field is guarded
// by the resource's own mutex (r.mu), NOT the receipt's mutex (rr.mu). A direct
// field read here races with Resource.Request writing status to
// ResourceStatusAwaitingProof when the receiver demands missing parts at the
// same moment the delivery proof arrives.
//
// The fix reads through resource.Status() (which takes r.mu). This test runs
// the callback concurrently with a goroutine that writes resource.status under
// r.mu, mirroring the concurrent writers. Under -race the reverted (direct
// field) read fails; the accessor read is clean.
func TestRequestResourceConcludedReadsStatusUnderResourceLock(t *testing.T) {
	t.Parallel()

	link := &Link{logger: NewLogger()}
	rr := &RequestReceipt{
		logger:    NewLogger(),
		Link:      link,
		RequestID: []byte("race-req"),
		Status:    RequestSent,
	}
	resource := &Resource{link: link}

	// Writer: mutate resource.status under r.mu, the way Resource.Request,
	// ValidateProof, and the watchdog do. Never Complete, so the callback takes
	// the failure branch (removePendingRequest on an empty list, no spawned
	// goroutines) and stays simple.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			resource.mu.Lock()
			resource.status = ResourceStatusAwaitingProof
			resource.mu.Unlock()
			resource.mu.Lock()
			resource.status = ResourceStatusFailed
			resource.mu.Unlock()
		}
	})

	// Reader: the callback reads resource status. With the fix it goes through
	// Status() (r.mu); a reverted direct field read races with the writer.
	for range 2000 {
		rr.requestResourceConcluded(resource)
	}
	close(stop)
	wg.Wait()
}
