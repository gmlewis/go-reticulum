// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// TestExpectedHopsSetOnInitiatorFromHopsTo asserts Phase 7 Task 1: the
// initiator-side link records expectedHops from the transport's path-table hop
// count at link-request time, mirroring RNS/Link.py:281
//
//	self.expected_hops = RNS.Transport.hops_to(self.destination.hash)
//
// When the path is unknown hops_to returns PathfinderM (128), so a fresh
// initiator link whose destination has no path-table entry starts at 128.
func TestExpectedHopsSetOnInitiatorFromHopsTo(t *testing.T) {
	t.Parallel()
	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)
	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	t.Cleanup(cleanup)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")

	// No announce / path exchange has occurred, so the initiator has no
	// path-table entry for the receiver: hops_to is PathfinderM (128).
	if got := tsInitiator.HopsTo(receiverDest.Hash); got != PathfinderM {
		t.Fatalf("HopsTo(receiver) = %v, want %v (PathfinderM, unknown path)", got, PathfinderM)
	}

	initiator := mustTestNewLink(t, tsInitiator, receiverDest)
	if got := initiator.ExpectedHops(); got != PathfinderM {
		t.Fatalf("initiator ExpectedHops() = %v, want %v (hops_to at request time)", got, PathfinderM)
	}
}

// TestLinkProofMismatchedHopsRejected asserts Phase 7 Task 1: an LRPROOF
// whose hop count differs from the link's expectedHops is rejected by the
// Transport delivery gate when the path re-balance cannot authorize it.
// A proof with a mismatched hop count and an invalid signature is dropped
// before reaching ValidateProof, mirroring RNS/Transport.py:2276-2312 where
// only proofs with packet.hops == link.expected_hops (after an optional
// signature-validated re-balance) are delivered to validate_proof.
//
// The observable: the link is not delivered the proof — its status stays
// LinkPending and lastInbound (set by Link.receive) is unchanged.
func TestLinkProofMismatchedHopsRejected(t *testing.T) {
	t.Parallel()
	ts := newTestTransportSystem(t)
	receiverDest := mustTestNewDestination(t, newTestTransportSystem(t), ts.identity, DestinationIn, DestinationSingle, "reject")

	// initiator link to the receiver destination (shares the identity for
	// signature verification). expectedHops starts at PathfinderM.
	link := mustTestNewLink(t, ts, receiverDest)
	// Force a known, small expected hop count so the proof's hops mismatch
	// without relying on the unknown-path default.
	link.SetExpectedHops(3)

	lastInboundBefore := link.lastInbound
	statusBefore := link.GetStatus()

	// Build a bogus LRPROOF: 96 bytes of zero data (signature+pub). The
	// signature will not verify, so the re-balance aborts and the gate
	// rejects the proof. Hops=5 mismatches expectedHops=3.
	proof := &Packet{
		PacketType:      PacketProof,
		Context:         ContextLrproof,
		DestinationHash: link.linkID,
		Hops:            5,
		Data:            make([]byte, 96),
	}
	ts.deliverLinkProof(link, proof)

	if got := link.GetStatus(); got != statusBefore || got != LinkPending {
		t.Errorf("link status = %v, want %v (LinkPending; proof rejected)", got, statusBefore)
	}
	if link.lastInbound != lastInboundBefore {
		t.Errorf("link lastInbound changed after rejected proof: %v -> %v (Link.receive should not run)", lastInboundBefore, link.lastInbound)
	}
	if got := link.ExpectedHops(); got != 3 {
		t.Errorf("link ExpectedHops = %v, want 3 (re-balance must not adopt hops for an invalid signature)", got)
	}
}

// TestLinkProofMismatchedHopsRejectedWhenRebalanceDisabled asserts Phase 7
// Task 1: when ALLOW_LINK_PATH_REBALANCE is disabled, a proof whose hop count
// mismatches expectedHops is rejected even when its signature is valid — the
// re-balance that would otherwise adopt the proof's hop count is skipped
// (RNS/Transport.py:2276 gates the re-balance on
// Transport.ALLOW_LINK_PATH_REBALANCE).
func TestLinkProofMismatchedHopsRejectedWhenRebalanceDisabled(t *testing.T) {
	t.Parallel()
	ts := newTestTransportSystem(t)
	receiverDest := mustTestNewDestination(t, newTestTransportSystem(t), ts.identity, DestinationIn, DestinationSingle, "rebal-disable")

	link := mustTestNewLink(t, ts, receiverDest)
	link.SetExpectedHops(3)

	// Construct a proof with a VALID signature over the exact signed_data
	// the re-balance re-validates (link_id + peer_pub + identity_sig_pub +
	// signalling_bytes), so verifyProofSignature returns true. With the
	// re-balance disabled the gate must still reject it.
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
		Hops:            5, // mismatches expectedHops=3
		Data:            proofData,
	}

	// Verify the signature really validates when re-balance is enabled, so
	// the test is meaningful (the rejection is due to the disabled flag,
	// not a bad signature).
	if !link.verifyProofSignature(proof) {
		t.Fatalf("setup invariant: verifyProofSignature should return true for the constructed proof")
	}

	prev := ts.AllowLinkPathRebalance()
	ts.SetAllowLinkPathRebalance(false)
	defer ts.SetAllowLinkPathRebalance(prev)

	lastInboundBefore := link.lastInbound
	ts.deliverLinkProof(link, proof)

	if got := link.ExpectedHops(); got != 3 {
		t.Errorf("link ExpectedHops = %v, want 3 (re-balance disabled must not adopt hops)", got)
	}
	if link.lastInbound != lastInboundBefore {
		t.Errorf("link lastInbound changed after rejected proof: %v -> %v", lastInboundBefore, link.lastInbound)
	}
	if got := link.GetStatus(); got != LinkPending {
		t.Errorf("link status = %v, want LinkPending (proof rejected)", got)
	}
}

// TestLinkProofRebalancedAndAcceptedOverLoopback asserts Phase 7 Task 1: a
// valid LRPROOF whose hop count mismatches the initiator's expectedHops
// (PathfinderM, unknown path) triggers a successful path re-balance — the
// link adopts the proof's hop count and the proof is delivered to
// ValidateProof, establishing the link. It also asserts the destination
// side records expectedHops from the RTT packet's hop count on activation
// (RNS/Link.py:525). Mirrors RNS/Transport.py:2276-2312 and Link.rtt_packet.
func TestLinkProofRebalancedAndAcceptedOverLoopback(t *testing.T) {
	t.Parallel()
	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)
	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	t.Cleanup(cleanup)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "rebalance")
	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) { establishedReceiver <- l }

	initiator := mustTestNewLink(t, tsInitiator, receiverDest)
	t.Cleanup(initiator.Teardown)
	if got := initiator.ExpectedHops(); got != PathfinderM {
		t.Fatalf("initiator ExpectedHops() before establish = %v, want %v (PathfinderM)", got, PathfinderM)
	}

	establishedInitiator := make(chan struct{}, 1)
	initiator.callbacks.LinkEstablished = func(*Link) { close(establishedInitiator) }

	if err := initiator.Establish(); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	select {
	case <-establishedInitiator:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}
	var receiver *Link
	select {
	case receiver = <-establishedReceiver:
		t.Cleanup(receiver.Teardown)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for receiver link establishment")
	}

	if initiator.status != LinkActive {
		t.Fatalf("initiator link not active: %v", initiator.status)
	}
	// The proof arrived over the direct pipe with hops=1, so the re-balance
	// adopted hops=1 (PathfinderM -> 1).
	if got := initiator.ExpectedHops(); got != 1 {
		t.Errorf("initiator ExpectedHops() after re-balance = %v, want 1 (proof hop count)", got)
	}
	if receiver.status != LinkActive {
		t.Fatalf("receiver link not active: %v", receiver.status)
	}
	// Destination side sets expected_hops = packet.hops at activation
	// (Link.py:525, rtt_packet). The RTT packet arrived with hops=1.
	if got := receiver.ExpectedHops(); got != 1 {
		t.Errorf("receiver ExpectedHops() after activation = %v, want 1 (RTT hop count)", got)
	}
}
