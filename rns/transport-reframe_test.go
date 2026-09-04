// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package rns

import (
	"bytes"
	"testing"
)

// TestReframeAnnounceForTransportHeader2 pins the announce rebroadcast
// framing parity: every announce a transport node emits must be rebuilt as a
// Header2 frame carrying THIS node's transport identity (Python
// Transport.py:637-649 rebuilds every queued rebroadcast as HEADER_2 with
// transport_id = Transport.identity.hash). Downstream nodes derive the path
// next-hop from that transport ID (Transport.py:1986), so an announce left as
// a Header1 frame makes them point the path at the DESTINATION hash; a later
// link request stamped with the destination hash as its transport ID matches
// no transport node and is silently dropped — multi-hop links never establish.
func TestReframeAnnounceForTransportHeader2(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	id, err := NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	ts.identity = id

	// A raw HEADER_1 announce frame as an originator emits it:
	// [flags=Header1|ANNOUNCE][hops=0][dest(16)][context=NONE][payload].
	dest := TruncatedHash([]byte("destination-material-00"))
	payload := []byte("announce-payload")
	raw := make([]byte, 0, 2+len(dest)+1+len(payload))
	raw = append(raw, byte(Header1<<6|PacketAnnounce), 0)
	raw = append(raw, dest...)
	raw = append(raw, ContextNone)
	raw = append(raw, payload...)

	got := ts.reframeAnnounceForTransport(raw, "test")

	parsed := &Packet{Raw: got}
	if err := parsed.Unpack(); err != nil {
		t.Fatalf("reframed frame does not unpack: %v", err)
	}
	if parsed.HeaderType != Header2 {
		t.Errorf("HeaderType = %d, want Header2 (%d)", parsed.HeaderType, Header2)
	}
	if parsed.PacketType != PacketAnnounce {
		t.Errorf("PacketType = %d, want ANNOUNCE (%d)", parsed.PacketType, PacketAnnounce)
	}
	if parsed.TransportID == nil || !bytes.Equal(parsed.TransportID, ts.identity.Hash) {
		t.Errorf("TransportID = %x, want this node's identity %x", parsed.TransportID, ts.identity.Hash)
	}
	if !bytes.Equal(parsed.DestinationHash, dest) {
		t.Errorf("DestinationHash = %x, want %x", parsed.DestinationHash, dest)
	}
	if !bytes.Equal(parsed.Data, payload) {
		t.Errorf("Data = %q, want %q (payload must survive untouched — the signature covers only the payload)", parsed.Data, payload)
	}
	if parsed.Hops != 0 {
		t.Errorf("Hops = %d, want 0 (hop count preserved)", parsed.Hops)
	}

	// A Header1 frame whose transport identity is already ours (an already
	// re-framed announce) must still come out with our identity intact.
	stamped := ts.reframeAnnounceForTransport(got, "test")
	if !bytes.Equal(stamped, got) {
		t.Errorf("double re-frame changed the frame:\n got2=%x\n want=%x", stamped, got)
	}

	// Non-announce frames pass through unchanged.
	plain := []byte{byte(Header1<<6 | PacketData), 0}
	plain = append(plain, dest...)
	plain = append(plain, ContextNone)
	plain = append(plain, []byte("data")...)
	if !bytes.Equal(ts.reframeAnnounceForTransport(plain, "test"), plain) {
		t.Error("non-announce frame was modified by reframeAnnounceForTransport")
	}
}
