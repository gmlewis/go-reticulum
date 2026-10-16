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
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// TestFirstHopTimeoutMatchesPython pins the establishment-timeout first-hop term
// against Python's Transport.first_hop_timeout (RNS/Transport.py:2786-2791):
//
//	latency    = next_hop_per_byte_latency(hash)   # (1/bitrate)*8
//	return MTU * latency + DEFAULT_PER_HOP_TIMEOUT
//
// So the term is MTU * 8 / bitrate — one MTU-sized packet's transmission time —
// not MTU / bitrate. The expected values below were captured by running that
// real function in the Python module against a synthetic next-hop bitrate:
//
//	bitrate=None      -> 6
//	bitrate=10000000  -> 6.0004
//	bitrate=1200      -> 9.333333333333334
//	bitrate=250       -> 22.0
//
// Dropping the factor of eight leaves the term 8x too small, so Go tears a link
// down about 14s before Python does on a 250bps next hop — a "Link establishment
// timed out" on a link Python would have established. The sibling
// extraLinkProofTimeout in the same file already applies the factor
// ((1/bitrate)*8*MTU).
func TestFirstHopTimeoutMatchesPython(t *testing.T) {
	t.Parallel()

	const (
		perHop = 6 * time.Second
	)

	tests := []struct {
		name    string
		bitrate int // 0 means "no next-hop interface known"
		want    time.Duration
	}{
		{"no interface falls back to the per-hop constant", 0, perHop},
		{"10 Mbps is negligible", 10_000_000, perHop + 400*time.Microsecond},
		{"1200 bps RNode", 1200, perHop + 3333333333*time.Nanosecond}, // 9.333333333s
		{"250 bps LoRa", 250, 22 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ts := NewTransportSystem(NewLogger())
			hash := []byte{0x11, 0x22, 0x33, 0x44}
			var iface interfaces.Interface
			if tc.bitrate > 0 {
				ci := &capturingInterface{name: "bitrate", bitrate: tc.bitrate}
				iface = ci
			}
			ts.mu.Lock()
			ts.pathTable[string(hash)] = &PathEntry{Interface: iface}
			ts.mu.Unlock()

			got := ts.FirstHopTimeout(hash)
			if diff := got - tc.want; diff > time.Microsecond || diff < -time.Microsecond {
				t.Errorf("FirstHopTimeout(bitrate=%v) = %v, want %v (Python Transport.first_hop_timeout)",
					tc.bitrate, got, tc.want)
			}
		})
	}
}
