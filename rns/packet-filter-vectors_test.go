// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// Golden vectors for Transport.packet_filter, derived fresh from the Python
// reference on every run.
//
// The filter is the transport's front door: it decides which frames are
// processed at all, and it is the only thing standing between a shared medium
// and a packet addressed to somebody else's transport instance. Python calls it
// at Transport.py:1525, before the hashlist remember at :1544, and its branches
// are, in order (Transport.py:1377-1428):
//
//	R1  a client of a shared instance filters nothing (the instance does it)
//	R2  a non-announce packet whose transport id names another instance is dropped
//	R3  keepalive, resource (request/proof/part), cache-request and channel
//	    contexts always pass
//	R4  a PLAIN destination: only a non-announce with at most one hop passes
//	R5  a GROUP destination: the same rule
//	R6  a hash never seen before passes
//	R7  a repeated hash passes only for a SINGLE-destination announce
//
// The table below drives every branch, and the hashlist dimension by pre-seeding
// the packet's own hash.

// packetFilterRequest is one filter scenario handed to Python.
type packetFilterRequest struct {
	Raw string `json:"raw"`
	// ConnectedToSharedInstance sets Transport.owner.connected_to_shared_instance.
	ConnectedToSharedInstance bool `json:"connected_to_shared_instance"`
	// HashAlreadySeen pre-seeds the packet hashlist (and the rotated list) with
	// the packet's own hash.
	HashAlreadySeen bool `json:"hash_already_seen"`
	// HashInPrevSeeds seeds only the rotated list.
	HashInPrevSeeds bool `json:"hash_in_prev"`
}

// pythonPacketFilterScript calls the reference Transport.packet_filter over each
// case and reports whether the packet would be accepted.
const pythonPacketFilterScript = `
import json, sys, types
import RNS
from RNS import Transport
from RNS.Packet import Packet as PacketCls

cases = json.load(open(sys.argv[1]))
Transport.identity = RNS.Identity.from_bytes(bytes(range(64)))
Transport.local_hops_delta = 0
Transport.local_client_interfaces = []

out = []
for c in cases:
    Transport.owner = types.SimpleNamespace(
        is_connected_to_shared_instance=c["connected_to_shared_instance"])
    raw = bytes.fromhex(c["raw"])
    p = PacketCls(None, raw)
    if not p.unpack():
        out.append({"accepted": None, "err": "unparseable"})
        continue
    Transport.packet_hashlist = set()
    Transport.packet_hashlist_prev = set()
    if c["hash_already_seen"]:
        Transport.packet_hashlist.add(p.packet_hash)
    if c["hash_in_prev"]:
        Transport.packet_hashlist_prev.add(p.packet_hash)
    rec = {"accepted": None, "err": ""}
    try:
        rec["accepted"] = bool(Transport.packet_filter(p))
    except Exception as e:
        rec["err"] = "%s: %s" % (type(e).__name__, e)
    out.append(rec)
print(json.dumps(out))
`

// packetFilterCase is one row of the Go table.
type packetFilterCase struct {
	name     string
	raw      []byte
	shared   bool
	seen     bool
	seenPrev bool
	citation string
}

// buildPacketFilterFrame assembles a frame with the given fields.
func buildPacketFilterFrame(flags byte, hops byte, transportID, destHash []byte, context byte, body []byte) []byte {
	frame := []byte{flags, hops}
	if transportID != nil {
		frame = append(frame, transportID...)
	}
	frame = append(frame, destHash...)
	frame = append(frame, context)
	return append(frame, body...)
}

// packetFilterCases enumerates every branch of the predicate.
func packetFilterCases() []packetFilterCase {
	ourTID := sequentialBytes(0x10, 16)
	foreignTID := sequentialBytes(0x20, 16)
	destHash := sequentialBytes(0x00, 16)
	body := []byte("filter-vector-body")

	// flags = header/contextFlag/transportType/destType/packetType
	flag := func(headerType, contextFlag, transportType, destType, packetType int) byte {
		return byte((headerType << 6) | (contextFlag << 5) | (transportType << 4) | (destType << 2) | packetType)
	}

	return []packetFilterCase{
		{
			name:     "r6_first_sight_single_data_passes",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1417 (a hash never seen before passes)",
		},
		{
			name:     "r7_repeat_hash_single_data_dropped",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextNone, body),
			seen:     true,
			citation: "RNS/Transport.py:1428 (a repeated hash is dropped unless it is a SINGLE announce)",
		},
		{
			name:     "r7_repeat_hash_in_prev_dropped",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextNone, body),
			seenPrev: true,
			citation: "RNS/Transport.py:1417 (the rotated list counts as seen)",
		},
		{
			name:     "r7_repeat_hash_single_announce_passes",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketAnnounce), 0, nil, destHash, ContextNone, body),
			seen:     true,
			citation: "RNS/Transport.py:1419-1423 (a repeated SINGLE announce passes, for replay protection upstream)",
		},
		{
			name:     "r7_repeat_hash_group_announce_dropped",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationGroup, PacketAnnounce), 0, nil, destHash, ContextNone, body),
			seen:     true,
			citation: "RNS/Transport.py:1424-1427 (a repeated non-SINGLE announce is dropped)",
		},
		{
			name: "r1_client_of_shared_instance_passes_everything",
			raw:  buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextNone, body),
			seen: true, shared: true,
			citation: "RNS/Transport.py:1379-1381 (a client of a shared instance leaves filtering to the instance)",
		},
		{
			name:     "r2_own_transport_id_passes",
			raw:      buildPacketFilterFrame(flag(Header2, 0, TransportForward, DestinationSingle, PacketData), 1, ourTID, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1383-1388 (the transport id is ours, so the packet is for this instance)",
		},
		{
			name:     "r2_foreign_transport_id_dropped",
			raw:      buildPacketFilterFrame(flag(Header2, 0, TransportForward, DestinationSingle, PacketData), 1, foreignTID, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1384-1387 (a non-announce packet for another transport instance is dropped)",
		},
		{
			name:     "r2_foreign_transport_id_announce_passes",
			raw:      buildPacketFilterFrame(flag(Header2, 0, TransportForward, DestinationSingle, PacketAnnounce), 1, foreignTID, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1384 (the transport-id check exempts announces)",
		},
		{
			name:     "r2_foreign_transport_id_link_request_dropped",
			raw:      buildPacketFilterFrame(flag(Header2, 0, TransportForward, DestinationSingle, PacketLinkRequest), 1, foreignTID, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1384-1387 (link requests are not exempt)",
		},
		{
			name:     "r3_keepalive_passes_even_as_a_repeat",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextKeepalive, body),
			seen:     true,
			citation: "RNS/Transport.py:1390-1391 (a keepalive is never dropped as a duplicate)",
		},
		{
			name:     "r3_resource_passes_even_as_a_repeat",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextResource, body),
			seen:     true,
			citation: "RNS/Transport.py:1394 (resource packets pass the filter)",
		},
		{
			name:     "r3_resource_req_passes_even_as_a_repeat",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextResourceReq, body),
			seen:     true,
			citation: "RNS/Transport.py:1392 (resource requests pass the filter)",
		},
		{
			name:     "r3_resource_prf_passes_even_as_a_repeat",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextResourcePrf, body),
			seen:     true,
			citation: "RNS/Transport.py:1393 (resource proofs pass the filter)",
		},
		{
			name:     "r3_cache_request_passes_even_as_a_repeat",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextCacheRequest, body),
			seen:     true,
			citation: "RNS/Transport.py:1395 (a cache request passes the filter)",
		},
		{
			name:     "r3_channel_passes_even_as_a_repeat",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationSingle, PacketData), 0, nil, destHash, ContextChannel, body),
			seen:     true,
			citation: "RNS/Transport.py:1396 (a channel packet passes the filter)",
		},
		{
			name:     "r4_plain_data_one_hop_passes",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationPlain, PacketData), 1, nil, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1398-1404 (a PLAIN packet with at most one hop passes)",
		},
		{
			name:     "r4_plain_data_three_hops_dropped",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationPlain, PacketData), 3, nil, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1400-1403 (a PLAIN packet past one hop is dropped, which bounds PLAIN flooding)",
		},
		{
			name:     "r4_plain_announce_dropped",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationPlain, PacketAnnounce), 0, nil, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1406-1407 (a PLAIN announce is always dropped)",
		},
		{
			name:     "r5_group_data_one_hop_passes",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationGroup, PacketData), 1, nil, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1409-1415 (a GROUP packet with at most one hop passes)",
		},
		{
			name:     "r5_group_data_three_hops_dropped",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationGroup, PacketData), 3, nil, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1411-1414 (a GROUP packet past one hop is dropped)",
		},
		{
			name:     "r5_group_announce_dropped",
			raw:      buildPacketFilterFrame(flag(Header1, 0, TransportBroadcast, DestinationGroup, PacketAnnounce), 0, nil, destHash, ContextNone, body),
			citation: "RNS/Transport.py:1416 (a GROUP announce is always dropped)",
		},
	}
}

// TestPacketFilterVectorsAgainstPython pins the transport's packet filter to the
// Python reference branch by branch.
func TestPacketFilterVectorsAgainstPython(t *testing.T) {
	t.Parallel()

	cases := packetFilterCases()
	req := make([]packetFilterRequest, 0, len(cases))
	for _, c := range cases {
		req = append(req, packetFilterRequest{
			Raw:                       hex.EncodeToString(c.raw),
			ConnectedToSharedInstance: c.shared,
			HashAlreadySeen:           c.seen,
			HashInPrevSeeds:           c.seenPrev,
		})
	}

	var results []struct {
		Accepted *bool  `json:"accepted"`
		Err      string `json:"err"`
	}
	runFramingVectorPython(t, pythonPacketFilterScript, req, &results)
	if len(results) != len(cases) {
		t.Fatalf("python returned %v results for %v cases", len(results), len(cases))
	}

	for i, c := range cases {
		res := results[i]
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if res.Err != "" {
				t.Fatalf("python failed for this case: %v", res.Err)
			}
			if res.Accepted == nil {
				t.Fatalf("python could not parse the frame")
			}

			ts := NewTransportSystem(nil)
			ts.identity = mustTestNewIdentity(t, true)
			ts.SetConnectedToSharedInstance(c.shared)

			packet := NewPacketFromRaw(append([]byte(nil), c.raw...))
			if err := packet.Unpack(); err != nil {
				t.Fatalf("unpack: %v", err)
			}
			// The Python side seeds the hashlist with the *parsed* packet's hash,
			// so the same hash has to go in here.
			ts.mu.Lock()
			ts.ensureStateLocked()
			if c.seen {
				ts.packetHashes[string(packet.PacketHash)] = time.Now()
			}
			if c.seenPrev {
				ts.packetHashesPrev[string(packet.PacketHash)] = time.Now()
			}
			got := ts.packetFilter(packet)
			ts.mu.Unlock()

			if got != *res.Accepted {
				t.Errorf("packetFilter = %v, want %v (%v)", got, *res.Accepted, c.citation)
			}
		})
	}
}

// TestPacketFilterVectorScriptIsVersionAware guards the harness itself: the
// vectors are meaningless if the interpreter cannot parse RNS's own Packet, so
// a silent "unparseable" answer fails loudly instead of passing.
func TestPacketFilterVectorScriptIsVersionAware(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	var results []struct {
		Accepted *bool  `json:"accepted"`
		Err      string `json:"err"`
	}
	req := []packetFilterRequest{{
		Raw: hex.EncodeToString(buildPacketFilterFrame(
			byte((Header1<<6)|(DestinationSingle<<2)|PacketData), 0, nil,
			sequentialBytes(0x00, 16), ContextNone, []byte("x"))),
	}}
	runFramingVectorPython(t, pythonPacketFilterScript, req, &results)
	if len(results) != 1 || results[0].Accepted == nil {
		t.Fatalf("the reference interpreter could not parse a well-formed frame: %v — the vectors would silently pass", results)
	}
}
