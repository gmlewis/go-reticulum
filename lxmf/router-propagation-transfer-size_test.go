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

// TestPropagationTransferSizeSetFromProgressCallback covers Phase 17 task 4: the
// message-get progress callback populates propagationTransferSize from the
// request receipt's response_size, mirroring Python's message_get_progress
// `if request_receipt.response_size: self.propagation_transfer_size =
// request_receipt.response_size` (LXMRouter.py:1646-1649, v1.1.0).
func TestPropagationTransferSizeSetFromProgressCallback(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	if got := router.PropagationTransferSize(); got != nil {
		t.Fatalf("PropagationTransferSize() initially=%v want nil", *got)
	}

	receipt := &rns.RequestReceipt{}
	receipt.SetResponseSizeForTest(12345)
	router.messageGetProgress(receipt)

	if got := router.PropagationTransferSize(); got == nil {
		t.Fatalf("PropagationTransferSize()=nil want 12345 after progress callback")
	} else if *got != 12345 {
		t.Fatalf("PropagationTransferSize()=%v want 12345", *got)
	}
	if got, want := router.PropagationTransferState(), PRReceiving; got != want {
		t.Fatalf("PropagationTransferState()=%v want %v (PR_RECEIVING)", got, want)
	}
}

// TestPropagationTransferSizeIgnoresZeroResponseSize covers Phase 17 task 4: a
// falsy response_size (None or zero) does not populate propagationTransferSize,
// matching Python's `if request_receipt.response_size:` truthy guard
// (LXMRouter.py:1649, v1.1.0).
func TestPropagationTransferSizeIgnoresZeroResponseSize(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	// A receipt with no recorded response size (None) leaves the size unset.
	router.messageGetProgress(&rns.RequestReceipt{})
	if got := router.PropagationTransferSize(); got != nil {
		t.Fatalf("PropagationTransferSize() after nil response_size=%v want nil", *got)
	}

	// A receipt whose response_size is zero is also falsy in Python, so the size
	// stays unset.
	zero := &rns.RequestReceipt{}
	zero.SetResponseSizeForTest(0)
	router.messageGetProgress(zero)
	if got := router.PropagationTransferSize(); got != nil {
		t.Fatalf("PropagationTransferSize() after zero response_size=%v want nil", *got)
	}
}

// TestPropagationTransferSizeClearedOnAcknowledgeSync covers Phase 17 task 4:
// acknowledge_sync_completion resets propagationTransferSize to nil (None),
// mirroring Python (LXMRouter.py:1656-1663, v1.1.0).
func TestPropagationTransferSizeClearedOnAcknowledgeSync(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	receipt := &rns.RequestReceipt{}
	receipt.SetResponseSizeForTest(9876)
	router.messageGetProgress(receipt)
	if got := router.PropagationTransferSize(); got == nil {
		t.Fatalf("PropagationTransferSize()=nil want 9876 after progress callback")
	}

	router.acknowledgeSyncCompletion(false, nil)
	if got := router.PropagationTransferSize(); got != nil {
		t.Fatalf("PropagationTransferSize() after acknowledge=%v want nil", *got)
	}
	if got := router.PropagationTransferProgress(); got != 0 {
		t.Fatalf("PropagationTransferProgress() after acknowledge=%v want 0", got)
	}
}

// TestPropagationTransferSizeResetOnRequestMessages covers Phase 17 task 4:
// request_messages_from_propagation_node resets propagationTransferSize to nil
// at the start of a new sync, mirroring Python (LXMRouter.py:506-507, v1.1.0).
// The reset runs before the no-propagation-node early return, so it applies
// even when no outbound node is configured.
func TestPropagationTransferSizeResetOnRequestMessages(t *testing.T) {
	t.Parallel()

	ts := newPropagationPacketCaptureTransport()
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	receipt := &rns.RequestReceipt{}
	receipt.SetResponseSizeForTest(5555)
	router.messageGetProgress(receipt)
	if got := router.PropagationTransferSize(); got == nil {
		t.Fatalf("PropagationTransferSize()=nil want 5555 after progress callback")
	}

	// No outbound propagation node is configured, so this returns early, but
	// only after resetting the transfer state fields at the top of the method.
	limit := 0
	router.RequestMessagesFromPropagationNode(&limit)
	if got := router.PropagationTransferSize(); got != nil {
		t.Fatalf("PropagationTransferSize() after request reset=%v want nil", *got)
	}
}
