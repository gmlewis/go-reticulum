// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
)

// advertisedResource builds a single-segment outgoing resource with exactly
// parts parts, each with a distinct map hash, on a link whose SDU the test
// controls. The data itself is never sent; only the advertisement is inspected.
func advertisedResource(t *testing.T, parts int) *Resource {
	t.Helper()

	link := testActiveResourceLink(t)
	link.mtu = 120
	link.mdu = 100

	r := &Resource{
		link:         link,
		initiator:    true,
		status:       ResourceStatusQueued,
		randomHash:   []byte{0x01, 0x02, 0x03, 0x04},
		totalParts:   parts,
		parts:        make([]*ResourcePart, parts),
		hashmap:      make([][]byte, parts),
		originalHash: bytes.Repeat([]byte{0xA5}, 32),
	}
	r.hash = bytes.Repeat([]byte{0x5A}, 32)
	for i := range parts {
		mh := []byte{byte(i), byte(i >> 8), byte(i >> 16), 0xC3}
		r.hashmap[i] = mh
		r.parts[i] = &ResourcePart{Index: i, MapHash: mh}
	}
	return r
}

// TestAdvertisementCarriesOneHashmapSegment pins the wire format that makes a
// resource with more than HASHMAP_MAX_LEN parts transferable at all.
//
// Python's ResourceAdvertisement.pack defaults to segment 0
// (Resource.py:1319-1324), so an advertisement never carries more than
// HASHMAP_MAX_LEN map hashes however many parts the resource has; the receiver
// asks for the remaining segments with a HASHMAP_IS_EXHAUSTED request and the
// sender answers with RESOURCE_HMU (Resource.py:1030-1062). The test also pins
// the segment metadata the wire format carries for a byte payload: N parts,
// L=1 segment, I=1, and O equal to the resource hash.
func TestAdvertisementCarriesOneHashmapSegment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		parts      int
		wantHashes int
	}{
		{"smaller than one segment", hashmapMaxLen - 1, hashmapMaxLen - 1},
		{"exactly one segment", hashmapMaxLen, hashmapMaxLen},
		{"more than one segment", hashmapMaxLen + 37, hashmapMaxLen},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := advertisedResource(t, tc.parts)
			packet, err := r.buildAdvertisementPacket()
			if err != nil {
				t.Fatalf("buildAdvertisementPacket: %v", err)
			}
			adv, err := UnpackResourceAdvertisement(packet.Data)
			if err != nil {
				t.Fatalf("UnpackResourceAdvertisement: %v", err)
			}

			if got := len(adv.M) / ResourceMapHashLen; got != tc.wantHashes {
				t.Errorf("advertised %v map hashes for %v parts, want %v (Python pack(0), Resource.py:1319-1324)",
					got, tc.parts, tc.wantHashes)
			}
			want := hashmapSegment(r.hashmap, 0)
			for i, mh := range want {
				if !bytes.Equal(adv.M[i*ResourceMapHashLen:(i+1)*ResourceMapHashLen], mh) {
					t.Fatalf("advertised map hash %v = %x, want %x", i,
						adv.M[i*ResourceMapHashLen:(i+1)*ResourceMapHashLen], mh)
				}
			}
			if adv.N != tc.parts {
				t.Errorf("N = %v, want %v", adv.N, tc.parts)
			}
			if adv.L != 1 || adv.I != 1 {
				t.Errorf("L=%v I=%v, want 1/1 for a byte payload (Resource.py:319-323)", adv.L, adv.I)
			}
			if !bytes.Equal(adv.O, r.originalHash) {
				t.Errorf("O = %x, want the resource's original hash %x", adv.O, r.originalHash)
			}
		})
	}
}
