// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "github.com/gmlewis/go-reticulum/rns/interfaces"

// TimebaseFromRandomBlob returns the announce-emission timebase encoded in a
// replay-protection random blob: Python's int.from_bytes(blob[5:10], "big")
// (RNS/Transport.py:3650-3651). A blob of five bytes or fewer yields 0, exactly
// as the empty Python slice does.
func TimebaseFromRandomBlob(randomBlob []byte) uint64 {
	if len(randomBlob) <= 5 {
		return 0
	}
	var timebase uint64
	for _, b := range randomBlob[5:min(len(randomBlob), 10)] {
		timebase = (timebase << 8) | uint64(b)
	}
	return timebase
}

// TimebaseFromRandomBlobs returns the maximum timebase across the given random
// blobs, mirroring Python Transport.timebase_from_random_blobs
// (RNS/Transport.py:3654-3660). Unlike the internal path-table helper, this
// exported form applies no plausibility filter: it reproduces the reference
// arithmetic exactly for callers that need it.
func TimebaseFromRandomBlobs(randomBlobs [][]byte) uint64 {
	var timebase uint64
	for _, blob := range randomBlobs {
		if emitted := TimebaseFromRandomBlob(blob); emitted > timebase {
			timebase = emitted
		}
	}
	return timebase
}

// AnnounceEmitted returns the emission timestamp carried by an announce
// packet, mirroring Python Transport.announce_emitted
// (RNS/Transport.py:3663-3667): the timestamp lives in the random blob that
// follows the public keys and name hash.
func AnnounceEmitted(packet *Packet) uint64 {
	return announceEmissionFromPacket(packet)
}

// NextHopPerBitLatency returns the per-bit latency of the next hop towards
// destHash in seconds, mirroring Python Transport.next_hop_per_bit_latency
// (RNS/Transport.py:3110-3113). ok is false when no path or no positive
// bitrate is known.
func (ts *TransportSystem) NextHopPerBitLatency(destHash []byte) (float64, bool) {
	entry := ts.GetPathEntry(destHash)
	if entry == nil || entry.Interface == nil {
		return 0, false
	}
	bitrate := entry.Interface.Bitrate()
	if bitrate <= 0 {
		return 0, false
	}
	return 1 / float64(bitrate), true
}

// NextHopPerByteLatency returns the per-byte latency (eight times the per-bit
// latency) of the next hop towards destHash, mirroring Python
// Transport.next_hop_per_byte_latency (RNS/Transport.py:3116-3119).
func (ts *TransportSystem) NextHopPerByteLatency(destHash []byte) (float64, bool) {
	perBit, ok := ts.NextHopPerBitLatency(destHash)
	if !ok {
		return 0, false
	}
	return perBit * 8, true
}

// ExtraLinkProofTimeout returns the extra seconds a link proof may take on the
// given interface: one MTU at its bitrate, mirroring Python
// Transport.extra_link_proof_timeout (RNS/Transport.py:3128-3130). It returns 0
// for a nil interface or one with no bitrate.
func ExtraLinkProofTimeout(iface interfaces.Interface) float64 {
	if iface == nil {
		return 0
	}
	bitrate := iface.Bitrate()
	if bitrate <= 0 {
		return 0
	}
	return (1 / float64(bitrate)) * 8 * float64(MTU)
}
