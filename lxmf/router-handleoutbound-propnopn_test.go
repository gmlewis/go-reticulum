// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestHandleOutboundPropagatedWithoutPropagationNodeErrors verifies that
// HandleOutbound rejects a propagated message when no outbound
// propagation node is configured, mirroring Python LXMRouter.handle_outbound
// (LXMRouter.py:1748-1750, v0.9.7+) which calls fail_message and raises
// IOError. The message is marked FAILED and is not queued.
func TestHandleOutboundPropagatedWithoutPropagationNodeErrors(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	if router.outboundPropagationNode != nil {
		t.Fatal("test precondition: outboundPropagationNode must be nil")
	}

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated

	err := router.HandleOutbound(msg)
	if err == nil {
		t.Fatal("HandleOutbound returned nil error for propagated message with no PN configured")
	}
	if !strings.Contains(err.Error(), "propagation node") {
		t.Fatalf("HandleOutbound error=%q, want substring %q", err.Error(), "propagation node")
	}

	// Mirrors Python fail_message: the message is marked FAILED.
	if msg.State() != StateFailed {
		t.Fatalf("message state=%v want=%v (StateFailed)", msg.State(), StateFailed)
	}

	// The rejected message is not queued for outbound or deferred-stamp processing.
	if len(router.pendingOutbound) != 0 {
		t.Fatalf("pendingOutbound length=%v want=0 (rejected message must not be queued)", len(router.pendingOutbound))
	}
	if len(router.pendingDeferredStamps) != 0 {
		t.Fatalf("pendingDeferredStamps length=%v want=0 (rejected message must not be deferred)", len(router.pendingDeferredStamps))
	}
}

// TestHandleOutboundPropagatedWithPropagationNodeSucceeds is the positive
// control: when a propagation node IS configured, HandleOutbound
// accepts a propagated message (no error) and queues it for deferred stamp
// generation.
func TestHandleOutboundPropagatedWithPropagationNodeSucceeds(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.hasPath = func(_ []byte) bool { return true }
	router.pathWaitSleep = func(_ time.Duration) {}

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	router.outboundPropagationNode = rns.CalculateHash(destID, AppName, "propagation")
	ts.Remember(nil, router.outboundPropagationNode, destID.GetPublicKey(), nil)

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound with PN configured returned unexpected error: %v", err)
	}
}
