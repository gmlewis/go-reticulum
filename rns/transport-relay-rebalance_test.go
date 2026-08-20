// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// makeRelayLRProof builds a syntactically valid LRPROOF packet whose signature
// verifies against the receiver's identity, signed over the exact signed_data
// the relay re-validation reconstructs (packet.destination_hash + peer_pub +
// identity_sig_pub + signalling_bytes — RNS/Transport.py:2225-2232). hops sets
// the proof's hop count; no MTU signalling extension is included.
func makeRelayLRProof(t *testing.T, receiverIdentity *Identity, linkID []byte, hops int) *Packet {
	t.Helper()
	peerPub := bytes.Repeat([]byte{0xCD}, 32)
	sigPub := receiverIdentity.GetPublicKey()[32:64]
	signedData := make([]byte, 0, len(linkID)+len(peerPub)+len(sigPub))
	signedData = append(signedData, linkID...)
	signedData = append(signedData, peerPub...)
	signedData = append(signedData, sigPub...)
	signature, err := receiverIdentity.Sign(signedData)
	if err != nil {
		t.Fatalf("identity.Sign: %v", err)
	}
	proofData := make([]byte, 0, len(signature)+len(peerPub))
	proofData = append(proofData, signature...)
	proofData = append(proofData, peerPub...)
	return &Packet{
		PacketType:      PacketProof,
		Context:         ContextLrproof,
		DestinationHash: linkID,
		Hops:            hops,
		Data:            proofData,
		Raw:             make([]byte, 16),
	}
}

// injectStaleRelayLinkEntry seeds the relay's link table with a not-yet-validated
// entry for linkID whose RemainingHops reflects the path hops recorded at link
// request time. It simulates the relay state mid-establishment, before a shorter
// return path is observed. The proof arrives on outIface (the interface toward
// the receiver) and is forwarded on fwdIface (the interface toward the initiator).
func injectStaleRelayLinkEntry(t *testing.T, ts *TransportSystem, linkID, destHash []byte, remainingHops int, outIface, fwdIface interfaces.Interface) {
	t.Helper()
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.linkTable[string(linkID)] = &LinkEntry{
		Timestamp:         time.Now(),
		OutboundInterface: outIface,
		ReceivedInterface: fwdIface,
		RemainingHops:     remainingHops,
		Hops:              0,
		DestinationHash:   destHash,
		Validated:         false,
		ProofTimeout:      time.Now().Add(time.Hour),
	}
}

// setRelayPathHops overrides the relay's path-table hop count for destHash,
// simulating the path hop count recorded at link request time (which may be
// longer than a shorter path that becomes available before the proof returns).
func setRelayPathHops(t *testing.T, ts *TransportSystem, destHash []byte, hops int) {
	t.Helper()
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.pathTable[string(destHash)] = &PathEntry{
		Hops:      hops,
		Timestamp: time.Now(),
		Expires:   time.Now().Add(time.Hour),
	}
}

// TestRelayLinkProofRebalanceOnShorterPath asserts that when a relay
// is transporting an LRPROOF for a remote link and the proof's hop count is
// smaller than the link-table entry's RemainingHops (a shorter path became
// available after the link request was forwarded), the relay re-validates the
// proof signature and rewrites both the link-table RemainingHops
// (IDX_LT_REM_HOPS) and the path-table Hops (IDX_PT_HOPS) to the proof's hop
// count, then forwards the validated proof. Mirrors RNS/Transport.py:2211-2265.
func TestRelayLinkProofRebalanceOnShorterPath(t *testing.T) {
	t.Parallel()
	tsRelay := NewTransportSystem(nil)
	tsReceiver := newTestTransportSystem(t)

	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "relay-rebalance")

	// The relay must be able to recall the receiver's identity public key to
	// re-validate the proof signature (RNS/Transport.py:2228
	// `RNS.Identity.recall(link_entry[IDX_LT_DSTHASH])`). An announce would
	// populate this in production; here we seed it directly.
	tsRelay.Remember(bytes.Repeat([]byte{0x77}, 16), receiverDest.Hash, tsReceiver.identity.GetPublicKey(), nil)

	linkID := bytes.Repeat([]byte{0x11}, 16)
	outIface := &capturingInterface{name: "toward-receiver"}
	fwdIface := &capturingInterface{name: "toward-initiator"}

	// At link-request time the relay recorded a 2-hop path to the receiver
	// (RemainingHops=2) and a 2-hop path-table entry. A 1-hop return path
	// becomes available before the proof returns, so the proof carries hops=1.
	injectStaleRelayLinkEntry(t, tsRelay, linkID, receiverDest.Hash, 2, outIface, fwdIface)
	setRelayPathHops(t, tsRelay, receiverDest.Hash, 2)

	proof := makeRelayLRProof(t, tsReceiver.identity, linkID, 1) // shorter path
	proof.ReceivingInterface = outIface

	ok := tsRelay.relayLinkProof(proof, outIface)
	if !ok {
		t.Fatal("relayLinkProof returned false; expected the relay to handle the proof")
	}

	tsRelay.mu.Lock()
	entry := tsRelay.linkTable[string(linkID)]
	pathEntry := tsRelay.pathTable[string(receiverDest.Hash)]
	tsRelay.mu.Unlock()

	if entry == nil {
		t.Fatal("link-table entry disappeared after re-balance")
	}
	if entry.RemainingHops != 1 {
		t.Errorf("link-table RemainingHops = %v, want 1 (re-balanced to proof hops)", entry.RemainingHops)
	}
	if !entry.Validated {
		t.Errorf("link-table Validated = false, want true (proof signature validated + forwarded)")
	}
	if pathEntry == nil {
		t.Fatal("path-table entry disappeared after re-balance")
	}
	if pathEntry.Hops != 1 {
		t.Errorf("path-table Hops = %v, want 1 (re-balanced to proof hops)", pathEntry.Hops)
	}
	if fwdIface.sendCount != 1 {
		t.Errorf("forward interface sendCount = %v, want 1 (validated proof forwarded toward initiator)", fwdIface.sendCount)
	}
}

// TestRelayLinkProofRebalanceAbortsOnInvalidSignature asserts that
// when the proof's hop count mismatches the link-table entry but the signature
// does not validate, the relay aborts the re-balance — the link-table and
// path-table hop counts are left unchanged and the proof is not forwarded
// (RNS/Transport.py:2237 "Aborting link request proof path re-balancing ...
// due to invalid signature").
func TestRelayLinkProofRebalanceAbortsOnInvalidSignature(t *testing.T) {
	t.Parallel()
	tsRelay := NewTransportSystem(nil)
	tsReceiver := newTestTransportSystem(t)
	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "relay-bad-sig")
	tsRelay.Remember(bytes.Repeat([]byte{0x77}, 16), receiverDest.Hash, tsReceiver.identity.GetPublicKey(), nil)

	linkID := bytes.Repeat([]byte{0x22}, 16)
	outIface := &capturingInterface{name: "toward-receiver"}
	fwdIface := &capturingInterface{name: "toward-initiator"}
	injectStaleRelayLinkEntry(t, tsRelay, linkID, receiverDest.Hash, 2, outIface, fwdIface)
	setRelayPathHops(t, tsRelay, receiverDest.Hash, 2)

	// A bogus proof: 96 zero bytes — the signature will not verify.
	bogus := &Packet{
		PacketType:      PacketProof,
		Context:         ContextLrproof,
		DestinationHash: linkID,
		Hops:            1, // mismatches RemainingHops=2
		Data:            make([]byte, 96),
		Raw:             make([]byte, 16),
	}
	bogus.ReceivingInterface = outIface

	tsRelay.relayLinkProof(bogus, outIface)

	tsRelay.mu.Lock()
	entry := tsRelay.linkTable[string(linkID)]
	pathEntry := tsRelay.pathTable[string(receiverDest.Hash)]
	tsRelay.mu.Unlock()

	if entry.RemainingHops != 2 {
		t.Errorf("link-table RemainingHops = %v, want 2 (re-balance must not adopt hops for an invalid signature)", entry.RemainingHops)
	}
	if entry.Validated {
		t.Errorf("link-table Validated = true, want false (invalid-signature proof must not be forwarded)")
	}
	if pathEntry.Hops != 2 {
		t.Errorf("path-table Hops = %v, want 2 (unchanged on aborted re-balance)", pathEntry.Hops)
	}
	if fwdIface.sendCount != 0 {
		t.Errorf("forward interface sendCount = %v, want 0 (proof not forwarded)", fwdIface.sendCount)
	}
}

// TestRelayLinkProofNoRebalanceWhenDisabled asserts that with
// ALLOW_LINK_PATH_REBALANCE disabled, a hop-mismatch proof is not re-balanced
// and is not transported (the hop-count match gate fails), even when the
// signature would validate. Mirrors RNS/Transport.py:2211 which gates the
// re-balance on Transport.ALLOW_LINK_PATH_REBALANCE.
func TestRelayLinkProofNoRebalanceWhenDisabled(t *testing.T) {
	t.Parallel()
	tsRelay := NewTransportSystem(nil)
	tsReceiver := newTestTransportSystem(t)
	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "relay-no-rebal")
	tsRelay.Remember(bytes.Repeat([]byte{0x77}, 16), receiverDest.Hash, tsReceiver.identity.GetPublicKey(), nil)

	linkID := bytes.Repeat([]byte{0x33}, 16)
	outIface := &capturingInterface{name: "toward-receiver"}
	fwdIface := &capturingInterface{name: "toward-initiator"}
	injectStaleRelayLinkEntry(t, tsRelay, linkID, receiverDest.Hash, 2, outIface, fwdIface)
	setRelayPathHops(t, tsRelay, receiverDest.Hash, 2)

	prev := tsRelay.AllowLinkPathRebalance()
	tsRelay.SetAllowLinkPathRebalance(false)
	defer tsRelay.SetAllowLinkPathRebalance(prev)

	proof := makeRelayLRProof(t, tsReceiver.identity, linkID, 1) // valid sig, hops mismatch
	proof.ReceivingInterface = outIface
	tsRelay.relayLinkProof(proof, outIface)

	tsRelay.mu.Lock()
	entry := tsRelay.linkTable[string(linkID)]
	pathEntry := tsRelay.pathTable[string(receiverDest.Hash)]
	tsRelay.mu.Unlock()

	if entry.RemainingHops != 2 {
		t.Errorf("link-table RemainingHops = %v, want 2 (re-balance disabled)", entry.RemainingHops)
	}
	if entry.Validated {
		t.Errorf("link-table Validated = true, want false (proof not transported on hop mismatch)")
	}
	if pathEntry.Hops != 2 {
		t.Errorf("path-table Hops = %v, want 2 (re-balance disabled)", pathEntry.Hops)
	}
	if fwdIface.sendCount != 0 {
		t.Errorf("forward interface sendCount = %v, want 0 (proof not forwarded)", fwdIface.sendCount)
	}
}

// TestRelayLinkProofForwardsOnMatchingHops asserts that when the
// proof's hop count already matches the link-table entry's RemainingHops
// (steady-state, no path change), the relay validates the signature and
// forwards the proof without altering either hop count.
func TestRelayLinkProofForwardsOnMatchingHops(t *testing.T) {
	t.Parallel()
	tsRelay := NewTransportSystem(nil)
	tsReceiver := newTestTransportSystem(t)
	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "relay-match")
	tsRelay.Remember(bytes.Repeat([]byte{0x77}, 16), receiverDest.Hash, tsReceiver.identity.GetPublicKey(), nil)

	linkID := bytes.Repeat([]byte{0x44}, 16)
	outIface := &capturingInterface{name: "toward-receiver"}
	fwdIface := &capturingInterface{name: "toward-initiator"}
	injectStaleRelayLinkEntry(t, tsRelay, linkID, receiverDest.Hash, 1, outIface, fwdIface)
	setRelayPathHops(t, tsRelay, receiverDest.Hash, 1)

	proof := makeRelayLRProof(t, tsReceiver.identity, linkID, 1) // hops match RemainingHops
	proof.ReceivingInterface = outIface
	ok := tsRelay.relayLinkProof(proof, outIface)
	if !ok {
		t.Fatal("relayLinkProof returned false; expected the relay to handle the proof")
	}

	tsRelay.mu.Lock()
	entry := tsRelay.linkTable[string(linkID)]
	pathEntry := tsRelay.pathTable[string(receiverDest.Hash)]
	tsRelay.mu.Unlock()

	if entry.RemainingHops != 1 {
		t.Errorf("link-table RemainingHops = %v, want 1 (unchanged; no re-balance)", entry.RemainingHops)
	}
	if !entry.Validated {
		t.Errorf("link-table Validated = false, want true (proof forwarded)")
	}
	if pathEntry.Hops != 1 {
		t.Errorf("path-table Hops = %v, want 1 (unchanged; no re-balance)", pathEntry.Hops)
	}
	if fwdIface.sendCount != 1 {
		t.Errorf("forward interface sendCount = %v, want 1 (proof forwarded)", fwdIface.sendCount)
	}
	// The forwarded raw carries the proof's hop count in the hop byte.
	if len(fwdIface.lastSent) < 2 || fwdIface.lastSent[1] != 1 {
		t.Errorf("forwarded raw hop byte = %v, want 1", fwdIface.lastSent)
	}
}
