// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestDeferPropagationStamp verifies that a propagated outbound message
// starts with DeferPropagationStamp set (stamp generation postponed), and
// Router.ProcessDeferredStamps generates the propagation stamp, clears the
// deferral flag, and appends the stamp to the propagation payload — mirroring
// Python LXMRouter.process_deferred_stamps (LXMRouter.py:2465-2498). The
// propagation-node announce app data carries the stamp cost (11), flexibility
// (256), and peering cost (7) captured from a matching Python propagation
// node, so the generated stamp must satisfy target cost 11.
func TestDeferPropagationStamp(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))

	sourceID := mustTestNewIdentity(t, true)
	destID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	destination := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	now := time.Unix(1700000000, 0).UTC()
	router.now = func() time.Time { return now }
	router.hasPath = func(_ []byte) bool { return false }
	router.requestPath = func(_ []byte) error { return nil }
	router.pathWaitSleep = func(time.Duration) {}

	router.outboundPropagationNode = rns.CalculateHash(destID, AppName, "propagation")
	appData, err := msgpack.Pack([]any{
		false,
		float64(now.Unix()),
		true,
		128,
		256,
		[]any{11, 3, 7},
		map[any]any{PNMetaName: []byte("Node A")},
	})
	if err != nil {
		t.Fatalf("Pack propagation app data: %v", err)
	}
	ts.Remember(nil, router.outboundPropagationNode, destID.GetPublicKey(), appData)

	msg := mustTestNewMessage(t, destination, sourceDest, "content", "title", nil)
	msg.DesiredMethod = MethodPropagated

	if err := router.HandleOutbound(msg); err != nil {
		t.Fatalf("HandleOutbound: %v", err)
	}

	// The propagation stamp must remain deferred until ProcessDeferredStamps.
	if !msg.DeferPropagationStamp {
		t.Fatal("expected DeferPropagationStamp=true after HandleOutbound (stamp generation deferred)")
	}
	if got := len(router.pendingDeferredStamps); got != 1 {
		t.Fatalf("pendingDeferredStamps length=%v want=1", got)
	}

	router.ProcessDeferredStamps()

	// Mirrors Python: deferral cleared and message moved to the outbound queue.
	if msg.DeferPropagationStamp {
		t.Fatal("expected DeferPropagationStamp=false after ProcessDeferredStamps")
	}
	if got := len(router.pendingDeferredStamps); got != 0 {
		t.Fatalf("pendingDeferredStamps length after processing=%v want=0", got)
	}
	if got := len(router.pendingOutbound); got != 1 {
		t.Fatalf("pendingOutbound length after processing=%v want=1", got)
	}
	if msg.PropagationTargetCost == nil || *msg.PropagationTargetCost != 11 {
		t.Fatalf("propagation target cost=%v want=11", msg.PropagationTargetCost)
	}
	if len(msg.PropagationStamp) != StampSize {
		t.Fatalf("propagation stamp length=%v want=%v", len(msg.PropagationStamp), StampSize)
	}
	workblock, err := StampWorkblock(msg.TransientID, WorkblockExpandRoundsPN)
	if err != nil {
		t.Fatalf("StampWorkblock: %v", err)
	}
	if !StampValid(msg.PropagationStamp, *msg.PropagationTargetCost, workblock) {
		t.Fatal("generated propagation stamp should satisfy propagation target cost 11")
	}

	// The propagation wire payload must carry the generated stamp as a suffix,
	// matching Python's pack() after defer_propagation_stamp is cleared.
	unpackedAny, err := msgpack.Unpack(msg.PropagationPacked)
	if err != nil {
		t.Fatalf("Unpack propagation payload: %v", err)
	}
	unpacked, ok := unpackedAny.([]any)
	if !ok || len(unpacked) != 2 {
		t.Fatalf("propagation payload=%#v want [timestamp, [lxmf_data]]", unpackedAny)
	}
	entries, ok := unpacked[1].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("propagation entries=%#v want single entry", unpacked[1])
	}
	lxmfData, ok := entries[0].([]byte)
	if !ok {
		t.Fatalf("propagation entry type=%T want []byte", entries[0])
	}
	if !bytes.HasSuffix(lxmfData, msg.PropagationStamp) {
		t.Fatalf("propagation payload=%x want suffix %x", lxmfData, msg.PropagationStamp)
	}
}
