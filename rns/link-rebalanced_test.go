// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// TestLinkRebalancedAtSetOnSuccessfulRebalance asserts Phase 7 Task 3: when a
// pending link's LRPROOF arrives with a hop count that mismatches expectedHops
// and the proof signature validates, the link records the re-balance timestamp
// (Python Transport.py:2298-2300 `if not link.rebalanced: link.rebalanced =
// time.time()`), adopts the proof's hop count, and then accepts the proof.
func TestLinkRebalancedAtSetOnSuccessfulRebalance(t *testing.T) {
	t.Parallel()
	ts := newTestTransportSystem(t)
	receiverDest := mustTestNewDestination(t, newTestTransportSystem(t), ts.identity, DestinationIn, DestinationSingle, "rebalanced")

	link := mustTestNewLink(t, ts, receiverDest)
	link.SetExpectedHops(3)

	// Construct a proof with a VALID signature over the exact signed_data the
	// re-balance re-validates (link_id + peer_pub + identity_sig_pub), so the
	// re-balance authorizes adopting the proof's hop count. Hops=5 mismatches
	// expectedHops=3.
	peerPub := make([]byte, 32)
	for i := range peerPub {
		peerPub[i] = 0xAB
	}
	identitySigPub := receiverDest.identity.GetPublicKey()[32:64]
	signedData := make([]byte, 0, len(link.linkID)+len(peerPub)+len(identitySigPub))
	signedData = append(signedData, link.linkID...)
	signedData = append(signedData, peerPub...)
	signedData = append(signedData, identitySigPub...)
	signature, err := receiverDest.identity.Sign(signedData)
	if err != nil {
		t.Fatalf("identity.Sign: %v", err)
	}
	proofData := make([]byte, 0, len(signature)+len(peerPub))
	proofData = append(proofData, signature...)
	proofData = append(proofData, peerPub...)

	proof := &Packet{
		PacketType:      PacketProof,
		Context:         ContextLrproof,
		DestinationHash: link.linkID,
		Hops:            5,
		Data:            proofData,
	}

	if zero := (time.Time{}); !link.RebalancedAt().Equal(zero) {
		t.Fatalf("link RebalancedAt before re-balance = %v, want zero", link.RebalancedAt())
	}

	ts.deliverLinkProof(link, proof)

	if got := link.ExpectedHops(); got != 5 {
		t.Errorf("link ExpectedHops = %v, want 5 (adopted proof hops)", got)
	}
	if link.RebalancedAt().Equal(time.Time{}) {
		t.Error("link RebalancedAt is zero after a successful re-balance; want the re-balance timestamp")
	}
}

// TestLinkRebalancedAtRecordedOnce asserts Phase 7 Task 3: the re-balance
// timestamp is recorded only on the FIRST successful re-balance. Python
// guards the assignment with `if not link.rebalanced:` (Transport.py:2298),
// so a subsequent re-balance (e.g. one whose full validate_proof fails and
// leaves the link pending, allowing a later proof to re-balance again) keeps
// the original timestamp. The guard is exercised directly via MarkRebalanced
// since a successful re-balance normally proceeds to validate_proof and
// activates the link, preventing a second deliverLinkProof call.
func TestLinkRebalancedAtRecordedOnce(t *testing.T) {
	t.Parallel()
	ts := newTestTransportSystem(t)
	receiverDest := mustTestNewDestination(t, newTestTransportSystem(t), ts.identity, DestinationIn, DestinationSingle, "rebalanced-once")

	link := mustTestNewLink(t, ts, receiverDest)
	link.SetExpectedHops(3)

	first := time.Now()
	link.MarkRebalanced(first)
	if got := link.RebalancedAt(); !got.Equal(first) {
		t.Fatalf("first MarkRebalanced: RebalancedAt = %v, want %v", got, first)
	}

	// A later re-balance must not overwrite the original timestamp.
	time.Sleep(15 * time.Millisecond)
	second := time.Now()
	link.MarkRebalanced(second)
	if got := link.RebalancedAt(); !got.Equal(first) {
		t.Errorf("second MarkRebalanced: RebalancedAt = %v, want %v (recorded only on the first re-balance)", got, first)
	}
}

// TestLinkRebalancedAtUnsetWhenRebalanceDisabled asserts Phase 7 Task 3: with
// ALLOW_LINK_PATH_REBALANCE disabled, a hop-mismatch proof is rejected and the
// re-balance timestamp is never recorded (the gate never reaches the
// signature-validated adoption).
func TestLinkRebalancedAtUnsetWhenRebalanceDisabled(t *testing.T) {
	t.Parallel()
	ts := newTestTransportSystem(t)
	receiverDest := mustTestNewDestination(t, newTestTransportSystem(t), ts.identity, DestinationIn, DestinationSingle, "rebalanced-disabled")

	link := mustTestNewLink(t, ts, receiverDest)
	link.SetExpectedHops(3)

	peerPub := make([]byte, 32)
	for i := range peerPub {
		peerPub[i] = 0xAB
	}
	identitySigPub := receiverDest.identity.GetPublicKey()[32:64]
	signedData := make([]byte, 0, len(link.linkID)+len(peerPub)+len(identitySigPub))
	signedData = append(signedData, link.linkID...)
	signedData = append(signedData, peerPub...)
	signedData = append(signedData, identitySigPub...)
	signature, err := receiverDest.identity.Sign(signedData)
	if err != nil {
		t.Fatalf("identity.Sign: %v", err)
	}
	proofData := append(append([]byte{}, signature...), peerPub...)
	proof := &Packet{PacketType: PacketProof, Context: ContextLrproof, DestinationHash: link.linkID, Hops: 5, Data: proofData}

	prev := ts.AllowLinkPathRebalance()
	ts.SetAllowLinkPathRebalance(false)
	defer ts.SetAllowLinkPathRebalance(prev)

	ts.deliverLinkProof(link, proof)

	if got := link.ExpectedHops(); got != 3 {
		t.Errorf("link ExpectedHops = %v, want 3 (re-balance disabled)", got)
	}
	if !link.RebalancedAt().Equal(time.Time{}) {
		t.Errorf("link RebalancedAt = %v, want zero (re-balance disabled, no timestamp recorded)", link.RebalancedAt())
	}
}
