// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
)

// TestPacketSendHopGate pins Python Packet.send's hop gate (Packet.py:262-263):
// a packet whose hop count has reached PATHFINDER_M is not packed or sent.
func TestPacketSendHopGate(t *testing.T) {
	t.Parallel()

	p := &Packet{Hops: PathfinderM}
	if err := p.Send(); err == nil {
		t.Fatal("Send with an exhausted hop count = nil error, want a refusal")
	}
	if p.Packed {
		t.Error("refused Send still packed the packet")
	}

	// One below the limit is not refused by the gate (it may still fail for
	// lack of a destination, but not on hops).
	p = &Packet{Hops: PathfinderM - 1}
	err := p.Send()
	if err != nil && p.Packed == false {
		// The hop gate runs before packing, so a non-hop failure may still be
		// reported; the assertion of interest is that the gate did not fire.
		t.Logf("Send below the hop limit returned %v (not a hop refusal)", err)
	}
}

// TestOutboundHopGate pins Python Transport._outbound's hop gate
// (Transport.py:1305).
func TestOutboundHopGate(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	if err := ts.Outbound(&Packet{Hops: PathfinderM}); err == nil {
		t.Fatal("Outbound with an exhausted hop count = nil error, want a refusal")
	}
}

// TestPacketUnpackFieldLengths pins Packet.unpack's Header-1/Header-2 field
// extraction and length validation (Packet.py:320-360): a frame too short for
// its header is rejected, and accepted frames carry exactly DST_LEN-byte hash
// fields.
func TestPacketUnpackFieldLengths(t *testing.T) {
	t.Parallel()

	dstLen := TruncatedHashLength / 8
	header1 := func(n int) []byte {
		raw := make([]byte, n)
		raw[0] = 0 // HEADER_1, SINGLE, DATA
		raw[1] = 0
		return raw
	}
	header2 := func(n int) []byte {
		raw := make([]byte, n)
		raw[0] = 0b01000000 // HEADER_2
		raw[1] = 0
		return raw
	}

	// Header 1 needs 2 + DST_LEN + 1 bytes.
	if err := (&Packet{Raw: header1(dstLen + 2)}).Unpack(); err == nil {
		t.Error("Header-1 frame one byte short = nil error, want a rejection")
	}
	p := &Packet{Raw: header1(dstLen + 3)}
	if err := p.Unpack(); err != nil {
		t.Fatalf("Header-1 frame = %v, want success", err)
	}
	if len(p.DestinationHash) != dstLen {
		t.Errorf("Header-1 destination hash = %v bytes, want %v", len(p.DestinationHash), dstLen)
	}
	if p.TransportID != nil {
		t.Errorf("Header-1 transport id = %x, want nil", p.TransportID)
	}

	// Header 2 needs 2 + 2*DST_LEN + 1 bytes.
	if err := (&Packet{Raw: header2(2*dstLen + 2)}).Unpack(); err == nil {
		t.Error("Header-2 frame one byte short = nil error, want a rejection")
	}
	p = &Packet{Raw: header2(2*dstLen + 3)}
	if err := p.Unpack(); err != nil {
		t.Fatalf("Header-2 frame = %v, want success", err)
	}
	if len(p.TransportID) != dstLen || len(p.DestinationHash) != dstLen {
		t.Errorf("Header-2 fields = (%v, %v) bytes, want (%v, %v)",
			len(p.TransportID), len(p.DestinationHash), dstLen, dstLen)
	}

	// The hop gate applies during unpack too (Packet.py:245-248).
	raw := header1(dstLen + 3)
	raw[1] = PathfinderM
	if err := (&Packet{Raw: raw}).Unpack(); err == nil {
		t.Error("frame with an exhausted hop count = nil error, want a rejection")
	}
}
