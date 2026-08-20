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

// localClientFakeInterface is a test-only Interface whose Type()/Name satisfy
// TransportSystem.isLocalClientInterface ("LocalInterface" + a "Local shared
// instance" name) without opening any real IPC channel, so tests can exercise
// the local-client path-request branch without touching the network.
type localClientFakeInterface struct {
	dummyInterface
}

func (l *localClientFakeInterface) Type() string { return "LocalInterface" }

// newLocalClientFake returns a fake interface that isLocalClientInterface
// recognises as a local shared-instance client link.
func newLocalClientFake() *localClientFakeInterface {
	return &localClientFakeInterface{dummyInterface: dummyInterface{name: "Local shared instance"}}
}

// useTimestampOf reads element 4 (the use-timestamp) of a known-destination
// entry, returning the numeric value and whether one was present.
func useTimestampOf(t *testing.T, ts *TransportSystem, destHash []byte) (float64, bool) {
	t.Helper()
	ts.mu.Lock()
	entry, ok := ts.knownDestinations[string(destHash)]
	ts.mu.Unlock()
	if !ok || len(entry) < 5 {
		return 0, false
	}
	return numericValue(entry[4])
}

// TestRelayLinkProofMarksDestinationUsed verifies that when a
// relay transports a link-request proof whose signature validates and whose hop
// count matches the link-table entry, the proof's destination is marked in-use
// (Python Transport.py:2263, `RNS.Identity._used_destination_data` after
// `Transport.transmit` of the validated proof, gated on
// `not is_connected_to_shared_instance`).
func TestRelayLinkProofMarksDestinationUsed(t *testing.T) {
	t.Parallel()
	tsRelay := NewTransportSystem(nil)
	tsReceiver := newTestTransportSystem(t)
	receiverDest := mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "used-dest-relay")
	tsRelay.Remember(bytes.Repeat([]byte{0x77}, 16), receiverDest.Hash, tsReceiver.identity.GetPublicKey(), nil)

	before, _ := useTimestampOf(t, tsRelay, receiverDest.Hash)
	if before != 0 {
		t.Fatalf("precondition: entry[4] = %v, want 0 (never used)", before)
	}

	linkID := bytes.Repeat([]byte{0x55}, 16)
	outIface := &capturingInterface{name: "toward-receiver"}
	fwdIface := &capturingInterface{name: "toward-initiator"}
	injectStaleRelayLinkEntry(t, tsRelay, linkID, receiverDest.Hash, 1, outIface, fwdIface)
	setRelayPathHops(t, tsRelay, receiverDest.Hash, 1)

	proof := makeRelayLRProof(t, tsReceiver.identity, linkID, 1)
	proof.ReceivingInterface = outIface
	if ok := tsRelay.relayLinkProof(proof, outIface); !ok {
		t.Fatal("relayLinkProof returned false; expected the relay to handle the proof")
	}

	got, ok := useTimestampOf(t, tsRelay, receiverDest.Hash)
	if !ok {
		t.Fatal("known-destination entry disappeared after relay link proof")
	}
	// Use a >0 check: the proof-validation use-marking stamps the current
	// time, replacing the never-used 0. See TestUnretainDestinationDataSetsUseTimestamp
	// for the float64-bucket rationale behind >0 (not >before).
	if got <= 0 {
		t.Fatalf("entry[4] = %v after relay link proof, want a positive use timestamp (marked used)", got)
	}
}

// TestPathRequestForLocalClientDestinationMarksUsed verifies that
// when a path request arrives for a destination whose known path lives on a
// local-client interface, the destination is marked in-use (Python
// Transport.py:3026, `_used_destination_data` inside the
// `destination_exists_on_local_client` registration block). The cached-path
// answer is made to abort via loop prevention (requestor == next hop) so only
// the local-client use-marking fires, isolating this call site from the
// cached-path-answer use-marking.
func TestPathRequestForLocalClientDestinationMarksUsed(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts.identity = mustTestNewIdentity(t, true)

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "used-dest-local-client")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	ts.Remember([]byte("pkt-lc"), dest.Hash, id.GetPublicKey(), nil)

	before, _ := useTimestampOf(t, ts, dest.Hash)
	if before != 0 {
		t.Fatalf("precondition: entry[4] = %v, want 0 (never used)", before)
	}

	// Install a cached path whose receiving interface is a local-client link.
	// NextHop is set to a transport ID equal to the requestor's transport ID
	// below, so the cached-path answer aborts on loop prevention and the
	// cached-path-answer use-marking does NOT fire — only the local-client
	// use-marking should.
	nextHop := bytes.Repeat([]byte{0xEE}, TruncatedHashLength/8)
	announce := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	ts.mu.Lock()
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Hops:       1,
		NextHop:    nextHop,
		Interface:  newLocalClientFake(),
		Packet:     copyBytes(announce.Raw),
		PacketHash: append([]byte(nil), announce.GetHash()...),
		Expires:    time.Now().Add(time.Hour),
	}
	ts.mu.Unlock()

	// data = destination_hash + requestor_transport_id (== nextHop) triggers
	// the loop-prevention abort in the cached-path answer branch.
	data := append([]byte(nil), dest.Hash...)
	data = append(data, nextHop...)
	pkt := &Packet{Data: data, ReceivingInterface: &capturingInterface{name: "requestor"}}

	ts.handlePathRequest(data, pkt)

	got, ok := useTimestampOf(t, ts, dest.Hash)
	if !ok {
		t.Fatal("known-destination entry disappeared after path request")
	}
	if got <= 0 {
		t.Fatalf("entry[4] = %v after local-client path request, want a positive use timestamp (marked used)", got)
	}
}

// TestAnnounceMatchingPendingPathRequestMarksUsed verifies that
// when an announce is received that installs a path for a destination this node
// has an outstanding path request for, the destination is marked in-use (Python
// Transport.py:2056, `if packet.destination_hash in Transport.path_requests:
// _used_destination_data`).
func TestAnnounceMatchingPendingPathRequestMarksUsed(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts.identity = mustTestNewIdentity(t, true)

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "used-dest-announce-pr")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	ts.Remember([]byte("pkt-pr"), dest.Hash, id.GetPublicKey(), nil)

	// Record an outstanding path request for this destination (this node has
	// requested a path to it), mirroring Transport.path_requests membership.
	ts.mu.Lock()
	ts.pathRequests[string(dest.Hash)] = time.Now()
	ts.mu.Unlock()

	before, _ := useTimestampOf(t, ts, dest.Hash)
	if before != 0 {
		t.Fatalf("precondition: entry[4] = %v, want 0 (never used)", before)
	}

	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	p.Hops = 1
	ts.handleAnnounce(p, &dummyInterface{name: "from-announce-pr"})

	got, ok := useTimestampOf(t, ts, dest.Hash)
	if !ok {
		t.Fatal("known-destination entry disappeared after announce")
	}
	if got <= 0 {
		t.Fatalf("entry[4] = %v after announce matching a pending PR, want a positive use timestamp (marked used)", got)
	}
}

// TestAnnounceWithoutPendingPathRequestDoesNotMarkUsed is the negative control
// for the announce-with-pending-PR use-marking: an announce that installs a path
// for a destination with NO outstanding path request must NOT mark the
// destination used (only the pending path-request membership gates the
// use-marking).
func TestAnnounceWithoutPendingPathRequestDoesNotMarkUsed(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts.identity = mustTestNewIdentity(t, true)

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "used-dest-announce-nopr")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	ts.Remember([]byte("pkt-nopr"), dest.Hash, id.GetPublicKey(), nil)

	// No pathRequests entry for this destination.
	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 2)
	p.Hops = 1
	ts.handleAnnounce(p, &dummyInterface{name: "from-announce-nopr"})

	got, ok := useTimestampOf(t, ts, dest.Hash)
	if !ok {
		t.Fatal("known-destination entry disappeared after announce")
	}
	if got != 0 {
		t.Fatalf("entry[4] = %v after announce with no pending PR, want 0 (not marked used)", got)
	}
}

// TestPathRequestAnsweredFromCachedPathMarksUsed verifies that
// when a path request is answered from a cached/known path, the destination is
// marked in-use (Python Transport.py:3097, `if not is_connected_to_shared_instance:
// _used_destination_data` after the announce_table insertion in the known-path
// answer branch). The cached path's interface is a non-local interface so the
// local-client use-marking does not fire, isolating this call site.
func TestPathRequestAnsweredFromCachedPathMarksUsed(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts.identity = mustTestNewIdentity(t, true)

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "used-dest-cached-answer")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	ts.Remember([]byte("pkt-cache"), dest.Hash, id.GetPublicKey(), nil)

	before, _ := useTimestampOf(t, ts, dest.Hash)
	if before != 0 {
		t.Fatalf("precondition: entry[4] = %v, want 0 (never used)", before)
	}

	// Install a cached path on a NON-local interface so the local-client
	// use-marking does not fire; NextHop differs from any requestor transport id
	// so the answer proceeds.
	announce := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	ts.mu.Lock()
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Hops:       2,
		NextHop:    bytes.Repeat([]byte{0xAA}, TruncatedHashLength/8),
		Interface:  &capturingInterface{name: "cached-from"},
		Packet:     copyBytes(announce.Raw),
		PacketHash: append([]byte(nil), announce.GetHash()...),
		Expires:    time.Now().Add(time.Hour),
	}
	ts.mu.Unlock()

	// data = destination_hash only (no requestor transport id), so loop
	// prevention is skipped and the cached-path answer proceeds.
	data := append([]byte(nil), dest.Hash...)
	recvIface := &capturingInterface{name: "requestor"}
	pkt := &Packet{Data: data, ReceivingInterface: recvIface}

	ts.handlePathRequest(data, pkt)

	got, ok := useTimestampOf(t, ts, dest.Hash)
	if !ok {
		t.Fatal("known-destination entry disappeared after cached path answer")
	}
	if got <= 0 {
		t.Fatalf("entry[4] = %v after cached-path answer, want a positive use timestamp (marked used)", got)
	}
	if recvIface.sendCount != 1 {
		t.Fatalf("requestor sendCount = %v, want 1 (cached path response sent)", recvIface.sendCount)
	}
}

// Compile-time check that the fake local-client interface satisfies the
// interfaces.Interface contract used by the transport path.
var _ interfaces.Interface = (*localClientFakeInterface)(nil)
