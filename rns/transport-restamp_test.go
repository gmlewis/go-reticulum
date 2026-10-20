// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"
)

// TestOutboundInsertsAtMostOneTransportHeader pins Python's transport-header
// rule for a packet that already has a known path (Transport.py:1148-1193):
//
//	if path_entry[IDX_PT_HOPS] > 1:
//	    if packet.header_type == RNS.Packet.HEADER_1:          # Header1 only
//	        ... insert the next hop as the transport id ...
//	elif path_entry[IDX_PT_HOPS] == 1 and owner.is_connected_to_shared_instance:
//	    if packet.header_type == RNS.Packet.HEADER_1:          # Header1 only
//	        ... insert the next hop ...
//	else:
//	    ... transmit the packet unchanged ...
//
// A Reticulum frame carries exactly ONE transport id, the immediate next hop.
// Python therefore inserts nothing into a packet that already arrives stamped
// HEADER_2 (which is what a co-located client of a shared instance builds, and
// what every packet of a multi-hop link carries), and transmits nothing at all
// for it. Prepending a second transport id in front of the existing one makes
// the next hop read that second id as the destination hash and drop the packet,
// so unicast traffic dies two hops out while announces (which never take this
// branch) keep flowing: links never establish, direct delivery silently fails
// and messages fall back to the propagation node.
func TestOutboundInsertsAtMostOneTransportHeader(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		stamped     bool // the packet arrives already carrying a transport id
		hops        int
		sharedInst  bool
		wantSent    bool // false: Python inserts no header and transmits nothing
		wantHeader2 bool
	}{
		{name: "header1_hops2_inserts_next_hop", hops: 2, wantSent: true, wantHeader2: true},
		{name: "header1_hops1_via_shared_instance_inserts_next_hop", hops: 1, sharedInst: true, wantSent: true, wantHeader2: true},
		{name: "header1_hops1_direct_transmits_unchanged", hops: 1, wantSent: true, wantHeader2: false},
		{name: "header2_hops2_is_never_restamped", stamped: true, hops: 2},
		{name: "header2_hops1_via_shared_instance_is_never_restamped", stamped: true, hops: 1, sharedInst: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ts := NewTransportSystem(nil)
			ts.identity = mustTestNewIdentity(t, true)
			ts.SetEnabled(true)
			ts.SetConnectedToSharedInstance(tc.sharedInst)

			iface := &capturingInterface{name: "out"}
			nextHop := mustTestNewIdentity(t, true).Hash
			dest := mustTestNewDestination(t, ts, ts.identity, DestinationOut, DestinationSingle, "testapp", "restamp")

			ts.mu.Lock()
			ts.pathTable[string(dest.Hash)] = &PathEntry{
				Interface: iface,
				Hops:      tc.hops,
				Timestamp: time.Now(),
				Expires:   time.Now().Add(time.Hour),
				NextHop:   append([]byte(nil), nextHop...),
			}
			ts.mu.Unlock()

			p := NewPacketWithTransport(ts, dest, []byte("hello there"))
			if tc.stamped {
				p.HeaderType = Header2
				p.TransportType = TransportForward
				p.TransportID = append([]byte(nil), ts.identity.Hash...)
			}
			if err := p.Pack(); err != nil {
				t.Fatalf("Pack: %v", err)
			}
			if err := ts.Outbound(p); err != nil {
				t.Fatalf("Outbound: %v", err)
			}

			hashLen := TruncatedHashLength / 8
			if !tc.wantSent {
				if n := iface.SendCount(); n != 0 {
					t.Fatalf("transmitted %v frame(s) for a packet that already carries a transport id; "+
						"Python inserts no header for a HEADER_2 packet and transmits nothing "+
						"(Transport.py:1149,1174)", n)
				}
				return
			}

			raw := iface.LastSent()
			if len(raw) < 2+2*hashLen {
				t.Fatalf("frame too short: %v bytes (%x)", len(raw), raw)
			}
			headerType := raw[0] >> 6
			if (headerType == Header2) != tc.wantHeader2 {
				t.Fatalf("header type = %v, want header2=%v (raw=%x)", headerType, tc.wantHeader2, raw[:2+hashLen])
			}
			destAt := 2
			if headerType == Header2 {
				if !bytes.Equal(raw[2:2+hashLen], nextHop) {
					t.Errorf("transport id = %x, want the path's next hop %x", raw[2:2+hashLen], nextHop)
				}
				destAt += hashLen
			}
			if !bytes.Equal(raw[destAt:destAt+hashLen], dest.Hash) {
				t.Errorf("destination hash at offset %v = %x, want %x: a frame carrying two transport ids "+
					"leaves the incoming transport id here instead, and the next hop drops the packet",
					destAt, raw[destAt:destAt+hashLen], dest.Hash)
			}
		})
	}
}
