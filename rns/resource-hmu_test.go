// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// mapHashAt builds a deterministic 4-byte map hash for part index i, so a test can name
// the part it asks about.
func mapHashAt(i int) []byte {
	return []byte{byte(i), byte(i >> 8), byte(i >> 16), 0x5A}
}

// TestHMUSegmentSelection pins the sender's collision-guard scan, mirroring the sender
// half of Python Resource.request (Resource.py:1002-1012):
//
//	part_index = receiver_min_consecutive_height
//	for part in parts[start : start+COLLISION_GUARD_SIZE]:
//	    part_index += 1
//	    if part.map_hash == last_map_hash: break
//	receiver_min_consecutive_height = max(part_index-1-WINDOW_MAX, 0)
//	segment = part_index // HASHMAP_MAX_LEN      (sequencing error unless part_index % MAX == 0)
//
// part_index is 1-based relative to the scan's start: finding the part at 0-based
// position i (with start 0) makes part_index i+1, so only positions 73, 147, … are
// segment boundaries.
func TestHMUSegmentSelection(t *testing.T) {
	t.Parallel()

	parts := make([]*ResourcePart, hashmapMaxLen*3)
	for i := range parts {
		parts[i] = &ResourcePart{MapHash: mapHashAt(i)}
	}

	tests := []struct {
		name        string
		index       int // 0-based position of the part whose hash is asked for
		startHeight int
		wantOK      bool
		wantSegment int
		wantHeight  int
	}{
		{"first segment boundary", 73, 0, true, 1, 0},
		{"second segment boundary", 147, 0, true, 2, max(148-1-resourceWindowMax, 0)},
		{"first part is not on a boundary", 0, 0, false, 0, 0},
		{"a part mid-segment is not on a boundary", 100, 0, false, 0, max(101-1-resourceWindowMax, 0)},
		{"a boundary reached from a later height", 147, 74, true, 2, max(148-1-resourceWindowMax, 0)},
		// A scan that finds nothing runs to its end (start + COLLISION_GUARD_SIZE, capped
		// at len(parts)) and still satisfies the check when that count is a multiple of
		// HASHMAP_MAX_LEN: 200 scanned to 222, and 222 = 3*74. Python behaves identically
		// — the sequencing check is on the count, not on whether a hash matched.
		{"an unmatched scan that ends on a multiple passes the check", 100, 200, true, 3, max(222-1-resourceWindowMax, 0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// When the scan starts past the asked-for part, no hash in range matches.
			lastMapHash := mapHashAt(tc.index)
			height, segment, ok := hmuSegmentForMapHash(parts, lastMapHash, tc.startHeight)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (segment %v, height %v)", ok, tc.wantOK, segment, height)
			}
			if !ok {
				return
			}
			if segment != tc.wantSegment {
				t.Errorf("segment = %v, want %v", segment, tc.wantSegment)
			}
			if height != tc.wantHeight {
				t.Errorf("receiver height = %v, want %v", height, tc.wantHeight)
			}
		})
	}
}

// TestHMUPayloadMatchesPython pins the RESOURCE_HMU packet body: the resource hash
// followed by the msgpack encoding of [segment, hashmap-segment-bytes]
// (Resource.py:1058-1059). The expected bytes come from the installed reference on every
// run, so neither the array shape nor the msgpack framing can drift.
func TestHMUPayloadMatchesPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	const parts = 150
	hashes := make([][]byte, parts)
	for i := range hashes {
		hashes[i] = mapHashAt(i)
	}
	r := &Resource{hash: bytes.Repeat([]byte{0xA7}, TruncatedHashLength/8), hashmap: hashes}

	req := map[string]any{
		"hash":    hex.EncodeToString(r.hash),
		"hashes":  hexList(hashes),
		"segment": 1,
	}
	in, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	dir := testutils.TempDir(t, tempDirPrefix)
	path := filepath.Join(dir, "hmu.json")
	if err := os.WriteFile(path, in, 0o644); err != nil {
		t.Fatal(err)
	}

	var wantHex string
	out := strings.TrimSpace(testutils.RunPython(t, pythonHMUPayloadScript, path))
	if err := json.Unmarshal([]byte(out), &wantHex); err != nil {
		t.Fatalf("decoding python output: %v\nraw: %v", err, out)
	}
	want, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.hmuPacketData(1)
	if err != nil {
		t.Fatalf("hmuPacketData: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("HMU payload = %x\nwant the reference's %x", got, want)
	}
}

// hexList renders hashes for the Python script.
func hexList(hashes [][]byte) []string {
	out := make([]string, len(hashes))
	for i, h := range hashes {
		out[i] = hex.EncodeToString(h)
	}
	return out
}

// pythonHMUPayloadScript builds the reference's HMU body for the same inputs.
const pythonHMUPayloadScript = `
import binascii, json, sys
from RNS.vendor import umsgpack
from RNS.Resource import Resource, ResourceAdvertisement

with open(sys.argv[1]) as f:
    req = json.load(f)

hashes = [bytes.fromhex(h) for h in req["hashes"]]
segment = req["segment"]
start = segment * ResourceAdvertisement.HASHMAP_MAX_LEN
end   = min((segment + 1) * ResourceAdvertisement.HASHMAP_MAX_LEN, len(hashes))
hashmap = b"".join(hashes[start:end])
body = bytes.fromhex(req["hash"]) + umsgpack.packb([segment, hashmap])
print(json.dumps(binascii.hexlify(body).decode()))
`
