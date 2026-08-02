// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestIngestLXMURIOutcome exercises the granular URI ingest path across all
// five outcomes, mirroring the Python LXMF Router.ingest_lxm_uri signal-string
// returns: a self-addressed paper URI is locally delivered (signal_local_delivery),
// re-ingesting it reports a duplicate (signal_duplicate), a non-local URI on a
// propagation-hosting router is stored (generic True), a non-local URI on a
// non-hosting router is discarded, and an undecodable URI reports None/error.
func TestIngestLXMURIOutcome(t *testing.T) {
	t.Parallel()

	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.stopJobLoop()

	// Local delivery destination.
	destID := mustTestNewIdentity(t, true)
	deliveryDest, err := router.RegisterDeliveryIdentity(destID, "", nil)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity: %v", err)
	}
	// Make the delivery destination's identity recallable by its hash so
	// UnpackMessageFromBytes can reconstruct it for verification.
	ts.Remember(nil, deliveryDest.Hash, destID.GetPublicKey(), nil)

	// Source destination (the peer who authored the paper message).
	sourceID := mustTestNewIdentity(t, true)
	sourceDest := mustTestNewDestination(t, ts, sourceID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	ts.Remember(nil, sourceDest.Hash, sourceID.GetPublicKey(), nil)

	// The sender packs a paper message addressed to the receiver's delivery
	// destination. CalculateHash is direction-independent, so a DestinationOut
	// built from the same identity shares the receiver's delivery-destination
	// hash, and Encrypt produces a static ECIES ciphertext the receiver can
	// decrypt with the delivery destination's private key.
	receiverOut := mustTestNewDestination(t, ts, destID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")

	msg := mustTestNewMessage(t, receiverOut, sourceDest, "local-delivery via uri", "title", nil)
	msg.DesiredMethod = MethodPaper
	if err := msg.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	uri, err := msg.AsURI(false)
	if err != nil {
		t.Fatalf("AsURI: %v", err)
	}

	var delivered *Message
	router.RegisterDeliveryCallback(func(m *Message) { delivered = m })

	// 1. Local delivery.
	outcome, err := router.IngestLXMURIOutcome(uri)
	if err != nil {
		t.Fatalf("IngestLXMURIOutcome local: %v", err)
	}
	if outcome != IngestOutcomeLocalDelivery {
		t.Fatalf("local outcome = %v, want IngestOutcomeLocalDelivery", outcome)
	}
	if delivered == nil {
		t.Fatal("expected delivery callback to fire for locally-addressed paper URI")
	}
	if delivered.ContentString() != "local-delivery via uri" {
		t.Errorf("delivered content = %q, want %q", delivered.ContentString(), "local-delivery via uri")
	}

	// 2. Duplicate.
	outcome, err = router.IngestLXMURIOutcome(uri)
	if err != nil {
		t.Fatalf("IngestLXMURIOutcome duplicate: %v", err)
	}
	if outcome != IngestOutcomeDuplicate {
		t.Fatalf("duplicate outcome = %v, want IngestOutcomeDuplicate", outcome)
	}

	// 3. Non-local URI on a non-hosting router → discarded.
	otherID := mustTestNewIdentity(t, true)
	otherDest := mustTestNewDestination(t, ts, otherID, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	otherMsg := mustTestNewMessage(t, otherDest, sourceDest, "not for me", "t", nil)
	otherMsg.DesiredMethod = MethodPaper
	if err := otherMsg.Pack(); err != nil {
		t.Fatalf("Pack other: %v", err)
	}
	otherURI, err := otherMsg.AsURI(false)
	if err != nil {
		t.Fatalf("AsURI other: %v", err)
	}
	outcome, err = router.IngestLXMURIOutcome(otherURI)
	if err != nil {
		t.Fatalf("IngestLXMURIOutcome discard: %v", err)
	}
	if outcome != IngestOutcomeDiscarded {
		t.Fatalf("discard outcome = %v, want IngestOutcomeDiscarded", outcome)
	}

	// 4. Non-local URI on a propagation-hosting router → propagated. Use a
	// fresh non-local message so it is not flagged as a duplicate of step 3.
	router.EnablePropagation()
	otherID2 := mustTestNewIdentity(t, true)
	otherDest2 := mustTestNewDestination(t, ts, otherID2, rns.DestinationOut, rns.DestinationSingle, AppName, "delivery")
	otherMsg2 := mustTestNewMessage(t, otherDest2, sourceDest, "also not for me", "t", nil)
	otherMsg2.DesiredMethod = MethodPaper
	if err := otherMsg2.Pack(); err != nil {
		t.Fatalf("Pack other2: %v", err)
	}
	otherURI2, err := otherMsg2.AsURI(false)
	if err != nil {
		t.Fatalf("AsURI other2: %v", err)
	}
	outcome, err = router.IngestLXMURIOutcome(otherURI2)
	if err != nil {
		t.Fatalf("IngestLXMURIOutcome propagate: %v", err)
	}
	if outcome != IngestOutcomePropagated {
		t.Fatalf("propagate outcome = %v, want IngestOutcomePropagated", outcome)
	}

	// 5. Undecodable URI → error + None.
	outcome, err = router.IngestLXMURIOutcome("lxm://!!!not-base64!!!")
	if err == nil {
		t.Fatal("expected error for undecodable URI")
	}
	if outcome != IngestOutcomeNone {
		t.Fatalf("none outcome = %v, want IngestOutcomeNone", outcome)
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v, want a decode error", err)
	}

	// 6. Bool IngestLXMURI still works (local delivery truthy).
	delivered = nil
	ok, err := router.IngestLXMURIAllowDuplicate(uri, true)
	if err != nil {
		t.Fatalf("IngestLXMURIAllowDuplicate: %v", err)
	}
	if !ok {
		t.Fatal("IngestLXMURIAllowDuplicate local = false, want true")
	}
	_ = bytes.Equal
}
