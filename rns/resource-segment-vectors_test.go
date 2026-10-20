// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestHashmapSegmentationAgainstPython pins the resource advertisement's hashmap
// segmentation against the reference. Python carries one segment of the map per
// advertisement, sized floor((Link.MDU - OVERHEAD)/MAPHASH_LEN), where Link.MDU is derived
// from the STATIC Reticulum MTU (Resource.py:1246, Link.py:73); `pack(segment)` slices
// [segment*max, min((segment+1)*max, n)) (Resource.py:1319-1324).
//
// The expected segment size and slices come from the installed reference on every run,
// not from a transcribed constant.
func TestHashmapSegmentationAgainstPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	// The battery: hashmap lengths around and across segment boundaries, and every
	// segment index each one reaches (plus one past the end).
	lengths := []int{0, 1, 73, 74, 75, 148, 149, 300}

	type req struct {
		Lengths []int `json:"lengths"`
	}
	in, err := json.Marshal(req{Lengths: lengths})
	if err != nil {
		t.Fatal(err)
	}

	var want struct {
		MaxLen   int     `json:"max_len"`
		Segments [][]int `json:"segments"`
	}
	// The cases go to Python through a file, the same way the framing vectors do, so a
	// large battery needs no shell quoting.
	dir := testutils.TempDir(t, tempDirPrefix)
	path := filepath.Join(dir, "segment-cases.json")
	if err := os.WriteFile(path, in, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout := testutils.RunPython(t, pythonSegmentScript, path)
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &want); err != nil {
		t.Fatalf("decoding python output: %v\nraw: %v", err, stdout)
	}

	// A positive assertion first: the reference must report a usable segment size, so a
	// broken interpreter cannot make the comparison vacuous.
	if want.MaxLen <= 0 {
		t.Fatalf("python reported HASHMAP_MAX_LEN = %v", want.MaxLen)
	}
	if hashmapMaxLen != want.MaxLen {
		t.Errorf("hashmapMaxLen = %v, want the reference's %v", hashmapMaxLen, want.MaxLen)
	}

	// One slice per (length, segment) pair, in the battery's order.
	idx := 0
	for _, n := range lengths {
		hashmap := make([][]byte, n)
		for i := range hashmap {
			hashmap[i] = []byte{byte(i), byte(i >> 8), 0xAA, 0x55}
		}
		for segment := 0; segment <= n/hashmapMaxLen+1; segment++ {
			if idx >= len(want.Segments) {
				t.Fatalf("python returned %v slices, need more", len(want.Segments))
			}
			got := hashmapSegment(hashmap, segment)
			wantIdx := want.Segments[idx]
			idx++
			if len(got) != len(wantIdx) {
				t.Errorf("n=%v segment=%v: %v hashes, want %v (indices %v)", n, segment, len(got), len(wantIdx), wantIdx)
				continue
			}
			for j, hash := range got {
				if i := int(hash[0]) | int(hash[1])<<8; i != wantIdx[j] {
					t.Errorf("n=%v segment=%v: hash %v is index %v, want %v", n, segment, j, i, wantIdx[j])
					break
				}
			}
		}
	}
}

// pythonSegmentScript reports the reference's HASHMAP_MAX_LEN and, for each battery
// length, the map-hash indices every segment carries — using the class's own constant and
// the same start/end expressions pack() uses.
const pythonSegmentScript = `
import json, sys
import RNS
from RNS.Resource import ResourceAdvertisement, Resource

with open(sys.argv[1]) as f:
    req = json.load(f)
max_len = ResourceAdvertisement.HASHMAP_MAX_LEN
segments = []
for n in req["lengths"]:
    for segment in range(0, n//max_len + 2):
        start = segment*max_len
        end   = min((segment+1)*max_len, n)
        segments.append(list(range(start, end)) if start < n else [])
print(json.dumps({"max_len": max_len, "segments": segments}))
`
