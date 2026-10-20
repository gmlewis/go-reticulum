// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestTransportHelpersPythonParity derives Python's timebase_from_random_blob,
// timebase_from_random_blobs and announce_emitted
// (RNS/Transport.py:3650-3667) for synthetic blobs and packet bodies. Python's
// announce_emitted only reads packet.data, so a bare object carrying that field
// exercises the reference function itself.
func TestTransportHelpersPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	blobs := [][]byte{
		{},
		{1, 2, 3, 4, 5},
		{1, 2, 3, 4, 5, 6},
		{0, 0, 0, 0, 0, 0x01, 0x02, 0x03, 0x04, 0x05},
		{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
	}
	pluralSets := [][][]byte{
		{},
		blobs[1:3],
		{blobs[0], blobs[3], blobs[4]},
	}
	// 64 key bytes + 10 name-hash bytes + 10 random-blob bytes, with the
	// timestamp at offset 74+5.
	packetData := make([]byte, 84)
	for i := range packetData {
		packetData[i] = byte(i)
	}
	packetData[79], packetData[80], packetData[81] = 0x65, 0x01, 0x02

	hexBlobs := make([]string, len(blobs))
	for i, b := range blobs {
		hexBlobs[i] = hex.EncodeToString(b)
	}
	hexPlurals := make([][]string, len(pluralSets))
	for i, set := range pluralSets {
		hexPlurals[i] = []string{}
		for _, b := range set {
			hexPlurals[i] = append(hexPlurals[i], hex.EncodeToString(b))
		}
	}
	in, err := json.Marshal(map[string]any{
		"blobs":   hexBlobs,
		"plurals": hexPlurals,
		"packets": []string{hex.EncodeToString(packetData), "", "0102"},
	})
	if err != nil {
		t.Fatal(err)
	}

	const script = `
import sys, json
import RNS
req = json.loads(sys.argv[1])
class P:
    pass
out = {
    "blobs": [RNS.Transport.timebase_from_random_blob(bytes.fromhex(b)) for b in req["blobs"]],
    "plurals": [RNS.Transport.timebase_from_random_blobs([bytes.fromhex(b) for b in s]) for s in req["plurals"]],
    "emitted": [],
}
for h in req["packets"]:
    p = P()
    p.data = bytes.fromhex(h)
    out["emitted"].append(RNS.Transport.announce_emitted(p))
json.dump(out, sys.stdout)
`
	var want struct {
		Blobs   []uint64 `json:"blobs"`
		Plurals []uint64 `json:"plurals"`
		Emitted []uint64 `json:"emitted"`
	}
	if err := json.Unmarshal([]byte(testutils.RunPython(t, script, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}

	for i, b := range blobs {
		if got := TimebaseFromRandomBlob(b); got != want.Blobs[i] {
			t.Errorf("TimebaseFromRandomBlob(%x) = %v, want %v (Python)", b, got, want.Blobs[i])
		}
	}
	for i, set := range pluralSets {
		if got := TimebaseFromRandomBlobs(set); got != want.Plurals[i] {
			t.Errorf("TimebaseFromRandomBlobs(%x) = %v, want %v (Python)", set, got, want.Plurals[i])
		}
	}
	packets := []*Packet{
		{Data: packetData},
		{Data: nil},
		{Data: []byte{1, 2}},
	}
	for i, p := range packets {
		if got := AnnounceEmitted(p); got != want.Emitted[i] {
			t.Errorf("AnnounceEmitted(case %v) = %v, want %v (Python)", i, got, want.Emitted[i])
		}
	}
}

// TestNextHopLatencyHelpers pins next_hop_per_bit/byte_latency and
// extra_link_proof_timeout (Transport.py:3110-3130).
func TestNextHopLatencyHelpers(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	dest := make([]byte, TruncatedHashLength/8)
	iface := newAFI("slow", 0)
	iface.bitrate = 1000
	ts.pathTable = map[string]*PathEntry{
		string(dest): {Interface: iface},
	}

	perBit, ok := ts.NextHopPerBitLatency(dest)
	if !ok || perBit != 1.0/1000 {
		t.Errorf("NextHopPerBitLatency = (%v, %v), want (0.001, true)", perBit, ok)
	}
	perByte, ok := ts.NextHopPerByteLatency(dest)
	if !ok || perByte != 8.0/1000 {
		t.Errorf("NextHopPerByteLatency = (%v, %v), want (0.008, true)", perByte, ok)
	}
	if _, ok := ts.NextHopPerBitLatency(bytes.Repeat([]byte{0xff}, 16)); ok {
		t.Error("NextHopPerBitLatency(unknown) ok = true, want false")
	}

	if got := ExtraLinkProofTimeout(iface); got != 8*float64(MTU)/1000 {
		t.Errorf("ExtraLinkProofTimeout = %v, want %v", got, 8*float64(MTU)/1000)
	}
	if got := ExtraLinkProofTimeout(nil); got != 0 {
		t.Errorf("ExtraLinkProofTimeout(nil) = %v, want 0", got)
	}
	zeroIface := newAFI("zero", 0)
	zeroIface.bitrate = 0
	if got := ExtraLinkProofTimeout(zeroIface); got != 0 {
		t.Errorf("ExtraLinkProofTimeout(zero bitrate) = %v, want 0", got)
	}
}
