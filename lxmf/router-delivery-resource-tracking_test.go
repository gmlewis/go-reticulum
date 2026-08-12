// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// newTrackedResource builds a bare rns.Resource with the given hash and status,
// suitable for the incoming-delivery-resource tracking tests (no link or
// watchdog, so Cancel is a safe no-op beyond the status flip).
func newTrackedResource(t *testing.T, hash []byte, status int) *rns.Resource {
	t.Helper()
	resource := &rns.Resource{}
	setResourceField(t, resource, "hash", hash)
	setResourceIntField(t, resource, "status", status)
	return resource
}

// TestDeliveryResourceTransferBeganTracksResource covers Phase 17 task 3:
// the delivery_resource_transfer_began callback records the incoming resource
// by its hash, so InboundCount and InboundResources report it while it is
// still active (LXMRouter.py:1968-1971,1671-1687, v1.1.0).
func TestDeliveryResourceTransferBeganTracksResource(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	hash := []byte("active-delivery-resource")
	resource := newTrackedResource(t, hash, rns.ResourceStatusTransferring)
	router.deliveryResourceTransferBegan(resource)

	if got, want := router.InboundCount(), 1; got != want {
		t.Fatalf("InboundCount()=%v want %v", got, want)
	}
	active := router.InboundResources()
	if len(active) != 1 {
		t.Fatalf("InboundResources()=%v want one resource", active)
	}
	if got := active[0].Hash(); string(got) != string(hash) {
		t.Fatalf("InboundResources()[0].Hash()=%x want %x", got, hash)
	}
}

// TestInboundCountExcludesTerminalResources covers Phase 17 task 3:
// InboundCount and InboundResources count only resources whose status is below
// COMPLETE, mirroring Python's inbound_count/inbound_resources
// (LXMRouter.py:1671-1687, v1.1.0).
func TestInboundCountExcludesTerminalResources(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	router.deliveryResourceTransferBegan(newTrackedResource(t, []byte("active"), rns.ResourceStatusTransferring))
	router.deliveryResourceTransferBegan(newTrackedResource(t, []byte("complete"), rns.ResourceStatusComplete))
	router.deliveryResourceTransferBegan(newTrackedResource(t, []byte("failed"), rns.ResourceStatusFailed))

	if got, want := router.InboundCount(), 1; got != want {
		t.Fatalf("InboundCount()=%v want %v (only the active resource)", got, want)
	}
	if got := len(router.InboundResources()); got != 1 {
		t.Fatalf("InboundResources()=%v want one active resource", got)
	}
}

// TestCancelInboundCancelsActiveResource covers Phase 17 task 3: CancelInbound
// cancels an active (non-terminal) incoming delivery resource and returns true;
// cancelling an unknown or already-concluded resource returns false
// (LXMRouter.py:1689-1706, v1.1.0).
func TestCancelInboundCancelsActiveResource(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	activeHash := []byte("cancel-me")
	router.deliveryResourceTransferBegan(newTrackedResource(t, activeHash, rns.ResourceStatusTransferring))

	if !router.CancelInbound(activeHash) {
		t.Fatalf("CancelInbound(%x) returned false for active resource; want true", activeHash)
	}
	if got, want := router.InboundCount(), 0; got != want {
		t.Fatalf("InboundCount() after cancel=%v want %v", got, want)
	}
	// The cancelled resource flips to FAILED (>= COMPLETE), so re-cancelling is a no-op.
	if router.CancelInbound(activeHash) {
		t.Fatalf("CancelInbound on already-concluded resource returned true; want false")
	}
	if router.CancelInbound([]byte("not-tracked")) {
		t.Fatalf("CancelInbound on unknown resource returned true; want false")
	}
}

// TestCancelAllInboundCancelsOnlyActive covers Phase 17 task 3: CancelAllInbound
// cancels every active incoming delivery resource and returns the count
// cancelled, leaving terminal resources untouched
// (LNMRouter.py:1708-1717, v1.1.0).
func TestCancelAllInboundCancelsOnlyActive(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	router.deliveryResourceTransferBegan(newTrackedResource(t, []byte("active-1"), rns.ResourceStatusTransferring))
	router.deliveryResourceTransferBegan(newTrackedResource(t, []byte("active-2"), rns.ResourceStatusAssembling))
	router.deliveryResourceTransferBegan(newTrackedResource(t, []byte("complete-1"), rns.ResourceStatusComplete))

	if got, want := router.CancelAllInbound(), 2; got != want {
		t.Fatalf("CancelAllInbound()=%v want %v", got, want)
	}
	if got, want := router.InboundCount(), 0; got != want {
		t.Fatalf("InboundCount() after cancel-all=%v want %v", got, want)
	}
}

// TestCleanResourceTrackingReapsTerminal covers Phase 17 task 3:
// CleanResourceTracking removes resources whose status has reached COMPLETE or
// above, while leaving active resources tracked, mirroring Python's
// clean_resource_tracking (LXMRouter.py:935-949, v1.1.0).
func TestCleanResourceTrackingReapsTerminal(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	router.deliveryResourceTransferBegan(newTrackedResource(t, []byte("active"), rns.ResourceStatusTransferring))
	router.deliveryResourceTransferBegan(newTrackedResource(t, []byte("complete"), rns.ResourceStatusComplete))
	router.deliveryResourceTransferBegan(newTrackedResource(t, []byte("failed"), rns.ResourceStatusFailed))

	router.CleanResourceTracking()

	router.incomingDeliveryResourcesMu.Lock()
	_, activePresent := router.incomingDeliveryResources[string([]byte("active"))]
	_, completePresent := router.incomingDeliveryResources[string([]byte("complete"))]
	_, failedPresent := router.incomingDeliveryResources[string([]byte("failed"))]
	router.incomingDeliveryResourcesMu.Unlock()

	if !activePresent {
		t.Fatalf("active resource was reaped; want retained")
	}
	if completePresent {
		t.Fatalf("complete resource was retained; want reaped")
	}
	if failedPresent {
		t.Fatalf("failed resource was retained; want reaped")
	}
	if got, want := router.InboundCount(), 1; got != want {
		t.Fatalf("InboundCount() after clean=%v want %v", got, want)
	}
}

// TestJobLoopRunsCleanResourceTracking covers Phase 17 task 3: the job loop
// invokes CleanResourceTracking on the JOB_RESOURCE_INTERVAL cadence (every
// 2 ticks, matching Python LXMRouter.JOB_RESOURCE_INTERVAL, v1.1.0). A
// terminal resource tracked via deliveryResourceTransferBegan is reaped once
// the loop advances to a clean tick, and not before.
func TestJobLoopRunsCleanResourceTracking(t *testing.T) {
	t.Parallel()

	if JOB_RESOURCE_INTERVAL != 2 {
		t.Fatalf("JOB_RESOURCE_INTERVAL=%v want 2 (matches Python)", JOB_RESOURCE_INTERVAL)
	}

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	// jobs() returns early when jobloopDone is nil (mustTestNewRouter stops the
	// loop). Install non-nil guard channels so direct jobs() calls proceed; nil
	// them out again before cleanup so the deferred Close's stopJobLoop is a
	// no-op (no goroutine ever closes the channel).
	router.mu.Lock()
	router.jobloopStop = make(chan struct{})
	router.jobloopDone = make(chan struct{})
	router.mu.Unlock()
	defer func() {
		router.mu.Lock()
		router.jobloopStop = nil
		router.jobloopDone = nil
		router.mu.Unlock()
	}()

	terminalHash := []byte("terminal-resource")
	router.deliveryResourceTransferBegan(newTrackedResource(t, terminalHash, rns.ResourceStatusComplete))

	// Tick 1: JOB_RESOURCE_INTERVAL == 2, so the clean job does not run yet.
	router.jobs()
	router.incomingDeliveryResourcesMu.Lock()
	_, present := router.incomingDeliveryResources[string(terminalHash)]
	router.incomingDeliveryResourcesMu.Unlock()
	if !present {
		t.Fatalf("terminal resource reaped after tick 1; want retained (clean runs every %d ticks)", JOB_RESOURCE_INTERVAL)
	}

	// Tick 2: the clean job runs and reaps the terminal resource.
	router.jobs()
	router.incomingDeliveryResourcesMu.Lock()
	present = router.incomingDeliveryResources[string(terminalHash)] != nil
	router.incomingDeliveryResourcesMu.Unlock()
	if present {
		t.Fatalf("terminal resource retained after tick %d; want reaped by clean job", JOB_RESOURCE_INTERVAL)
	}
}