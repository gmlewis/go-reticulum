// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// TestAnnounceRebroadcastHopCountParity verifies that the hop count baked
// into a queued announce rebroadcast matches Python Reticulum's behavior.
//
// In Python (Transport.py:1927, 632), announce_hops = packet.hops (the
// already-incremented value from inbound), and the rebroadcast packet's
// raw[1] = announce_hops. So a 0-hop announce on the wire becomes hops=1
// after inbound++, and the rebroadcast carries raw[1]=1.
//
// The Go port previously set raw[1] = packet.Hops + 1 (double-incrementing),
// causing every rebroadcast to carry one extra hop. This compounds across
// multi-hop paths: N actual hops show as 2N-1 in Go vs N in Python.
//
// This test injects a valid announce with wire hops=0 on a mock interface
// and asserts the queued announceTable entry's PacketRaw[1] == 1 (matching
// Python), not 2 (the pre-fix Go behavior).
func TestAnnounceRebroadcastHopCountParity(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)

	iface := &capturingInterface{name: "rx", gravity: 0}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "hopcount-parity")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// Build a valid announce with a plausible emission timebase (near local
	// time) so the poison-heal plausibility gate stores its blob like it would
	// for a real announce.
	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, uint64(time.Now().Unix()))
	p.Hops = 0 // wire hops = 0 (originating node)
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	raw := append([]byte(nil), p.Raw...)

	// Inject via Inbound: packet.Hops becomes 1 (Inbound does Hops++).
	ts.Inbound(raw, iface)

	// The announce should be queued for rebroadcast in the announce table.
	ts.mu.Lock()
	entry, ok := ts.announceTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("expected announce table entry after inbound announce")
	}

	// Python: announce_hops = packet.hops = 1, raw[1] = 1.
	// Go pre-fix: raw[1] = packet.Hops + 1 = 2 (BUG).
	if len(entry.PacketRaw) < 2 {
		t.Fatalf("PacketRaw too short: %v", entry.PacketRaw)
	}
	gotHops := int(entry.PacketRaw[1])
	wantHops := 1 // packet.Hops after inbound++ = 1, matching Python's announce_hops
	if gotHops != wantHops {
		t.Errorf("rebroadcast raw hop count = %v, want %v (Python: announce_hops = packet.hops after inbound++)",
			gotHops, wantHops)
	}
}

// TestAnnounceRebroadcastHopCountParityMultiHop verifies the hop count for a
// 2-hop announce (wire hops=1, inbound++ makes it 2). Python queues a
// rebroadcast with raw[1]=2; Go pre-fix queued raw[1]=3.
func TestAnnounceRebroadcastHopCountParityMultiHop(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)

	iface := &capturingInterface{name: "rx2", gravity: 0}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "hopcount-parity-2")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, uint64(time.Now().Unix()))
	p.Hops = 1 // wire hops = 1 (one hop away from originator)
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	raw := append([]byte(nil), p.Raw...)

	ts.Inbound(raw, iface)

	ts.mu.Lock()
	entry, ok := ts.announceTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("expected announce table entry after inbound announce")
	}

	if len(entry.PacketRaw) < 2 {
		t.Fatalf("PacketRaw too short: %v", entry.PacketRaw)
	}
	gotHops := int(entry.PacketRaw[1])
	wantHops := 2 // packet.Hops after inbound++ = 2
	if gotHops != wantHops {
		t.Errorf("rebroadcast raw hop count = %v, want %v (Python: announce_hops = packet.hops after inbound++)",
			gotHops, wantHops)
	}
}

// TestCachedPathResponseHopCountParity verifies that a cached path response
// carries the cached path's hop count in raw[1] (matching Python
// Transport.py:3049 where packet.hops = path_table[IDX_PT_HOPS]), not
// cached_hops+1 (the pre-fix Go behavior at transport.go:3053).
//
// When a node answers a path request from its cached path table, Python
// sets packet.hops = cached_hops and sends it. The requestor receives it,
// increments by 1 in inbound, and stores cached_hops+1. Go pre-fix set
// raw[1] = cached_hops+1, so the requestor stored cached_hops+2 — one
// extra hop inflated per cached path response.
func TestCachedPathResponseHopCountParity(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	ifaceRx := &capturingInterface{name: "path-rx", gravity: 0}
	ifaceOut := &capturingInterface{name: "path-out", gravity: 0}

	// Register the receiving interface so the transport knows about it.
	ts.RegisterInterface(ifaceRx)

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "cached-path-parity")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// Inject an announce with wire hops=3 to install a path with hops=4
	// (after inbound++). This is our "cached path."
	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, uint64(time.Now().Unix()))
	p.Hops = 3
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	raw := append([]byte(nil), p.Raw...)
	ts.Inbound(raw, ifaceRx)

	// Verify the path was installed with hops=4.
	ts.mu.Lock()
	pathEntry, ok := ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("expected path table entry after announce")
	}
	if pathEntry.Hops != 4 {
		t.Fatalf("path table hops = %v, want 4 (wire=3 + inbound++)", pathEntry.Hops)
	}

	// Now simulate a path request for this destination. The handler should
	// answer from the cached path. We capture the response on ifaceOut.
	//
	// Build a path request packet: destination_hash + requestor_transport_id + tag.
	// The requestor_transport_id is a random hash that is NOT the next hop,
	// so the loop-prevention check does not block the response.
	hashLen := TruncatedHashLength / 8
	pathReqData := make([]byte, 0, hashLen*3)
	pathReqData = append(pathReqData, dest.Hash...)             // target hash
	pathReqData = append(pathReqData, make([]byte, hashLen)...) // requestor transport ID (all zeros = not next hop)
	pathReqData = append(pathReqData, make([]byte, hashLen)...) // tag

	// Create the path request destination and packet callback.
	pathReqDst, err := NewDestination(ts, nil, DestinationOut, DestinationPlain, "rnstransport", "path", "request")
	if err != nil {
		t.Fatalf("NewDestination path request: %v", err)
	}

	// Build a packet that simulates the path request arriving on ifaceOut.
	reqPkt := NewPacket(pathReqDst, pathReqData)
	reqPkt.PacketType = PacketData
	reqPkt.ReceivingInterface = ifaceOut
	if err := reqPkt.Pack(); err != nil {
		t.Fatalf("Pack path request: %v", err)
	}

	// Call handlePathRequest directly (it's the packet callback).
	// The response should be sent on ifaceOut (the receiving interface).
	result := ts.handlePathRequest(pathReqData, reqPkt)
	if !result {
		t.Fatal("handlePathRequest returned false (did not handle the request)")
	}

	// The response should have been sent on ifaceOut.
	if ifaceOut.sendCount == 0 {
		t.Fatal("expected path response to be sent on ifaceOut")
	}

	sentRaw := ifaceOut.lastSent
	if len(sentRaw) < 2 {
		t.Fatalf("response raw too short: %v", sentRaw)
	}

	// Python: packet.hops = cached_hops = 4, raw[1] = 4.
	// Go pre-fix: raw[1] = cached_hops + 1 = 5 (BUG).
	gotHops := int(sentRaw[1])
	wantHops := 4 // cachedPath.Hops = 4, matching Python's packet.hops = path_table[IDX_PT_HOPS]
	if gotHops != wantHops {
		t.Errorf("cached path response raw hop count = %v, want %v (Python: packet.hops = cached_hops, not cached_hops+1)",
			gotHops, wantHops)
	}
}
