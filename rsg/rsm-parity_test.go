// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rsg

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// rsmParityInputs is the shared set of signing inputs (deterministic
// private key, notes/message, and manifest meta in Python dict insertion
// order) used by both the live Python capture and the Go CreateWithOptions
// call below. Ed25519 signing is deterministic, so byte-identity depends
// entirely on the msgpack envelope key matching Python's dict insertion
// order: hashtype, hash, meta(signer, pubkey, ...user meta...), [message].
const rsmParityPrvHex = "c8699e62f1d5354063c63c3c7165b1d6f3c0dd40d51a728be403635c3bef3a6cb6d658a728a5eccc2a6b69d15787cca20260782442a3f1c408d880a507f23e80"

const rsmParityNotesHex = "52656c65617365206e6f74657320666f7220312e302e300a496e697469616c2072656c656173652e"
const rsmParityArtifactHex = "61727469666163742062797465732068657265"

const rsmParityReleaseTime = 1718000000

// rsmParityPython returns the hex of Python's RNS.Utilities.rnid.create_rsg
// for both the embedded (RSM) and plain (RSG) forms, captured live from the
// installed RNS. It builds the manifest meta as a regular Python dict whose
// insertion order matches the Go OrderedMap in the calling test.
func rsmParityPython(t *testing.T) (rsmHex, rsgHex, signerHashHex string) {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)

	script := fmt.Sprintf(`
import RNS
from RNS.Utilities import rnid

prv = bytes.fromhex(%[1]q)
signer = RNS.Identity.from_bytes(prv)
print("SIGNER="+signer.hash.hex())

notes = bytes.fromhex(%[2]q)
releaseTime = %[3]d
origin = bytes.fromhex("00112233445566778899aabbccddeeff")
commit = "abc123def4567890abc123def4567890abc123de"

manifestMeta = {
    "name": "pkg",
    "version": "1.0.0",
    "released": "2024-06-10T06:13:20Z",
    "timestamp": releaseTime,
    "origin": origin,
    "path": "main/testrepo.git",
    "commit": commit,
    "artifacts": [{"name": "binary.bin", "rsg": b'\xde\xad\xbe\xef'}],
}

rsm = rnid.create_rsg(signer, notes, embed=True, meta=manifestMeta, output="bin")
print("RSM="+rsm.hex())

artifact = bytes.fromhex(%[4]q)
rsgBlob = rnid.create_rsg(signer, artifact, embed=False, meta={"timestamp": releaseTime}, output="bin")
print("RSG="+rsgBlob.hex())
`, rsmParityPrvHex, rsmParityNotesHex, rsmParityReleaseTime, rsmParityArtifactHex)

	out := testutils.RunPython(t, script)
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "SIGNER="):
			signerHashHex = strings.TrimPrefix(line, "SIGNER=")
		case strings.HasPrefix(line, "RSM="):
			rsmHex = strings.TrimPrefix(line, "RSM=")
		case strings.HasPrefix(line, "RSG="):
			rsgHex = strings.TrimPrefix(line, "RSG=")
		}
	}
	if rsmHex == "" || rsgHex == "" || signerHashHex == "" {
		t.Fatalf("python capture missing output:\n%s", out)
	}
	return rsmHex, rsgHex, signerHashHex
}

// rsmParitySigner loads the deterministic test identity shared with Python.
func rsmParitySigner(t *testing.T) *rns.Identity {
	t.Helper()
	prv, err := hex.DecodeString(rsmParityPrvHex)
	if err != nil {
		t.Fatalf("decode prv: %v", err)
	}
	signer, err := rns.FromBytes(prv, nil)
	if err != nil {
		t.Fatalf("FromBytes: %v", err)
	}
	return signer
}

// rsmParityManifestMeta builds the manifest meta OrderedMap whose key order
// matches the Python dict in rsmParityPython.
func rsmParityManifestMeta() msgpack.OrderedMap {
	origin, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	commit := "abc123def4567890abc123def4567890abc123de"
	return msgpack.OrderedMap{
		{Key: "name", Value: "pkg"},
		{Key: "version", Value: "1.0.0"},
		{Key: "released", Value: "2024-06-10T06:13:20Z"},
		{Key: "timestamp", Value: uint64(rsmParityReleaseTime)},
		{Key: "origin", Value: origin},
		{Key: "path", Value: "main/testrepo.git"},
		{Key: "commit", Value: commit},
		{Key: "artifacts", Value: []msgpack.OrderedMap{
			{
				{Key: "name", Value: "binary.bin"},
				{Key: "rsg", Value: []byte{0xde, 0xad, 0xbe, 0xef}},
			},
		}},
	}
}

// TestRSMByteParityWithPython asserts that rsg.CreateWithOptions produces
// a byte-identical RSM (embedded manifest) to Python's create_rsg(embed=True)
// for the same signer private key and the same meta key insertion order,
// captured live from the installed RNS.Utilities.rnid.
func TestRSMByteParityWithPython(t *testing.T) {
	wantRSMHex, _, wantSignerHex := rsmParityPython(t)

	signer := rsmParitySigner(t)
	wantSigner, _ := hex.DecodeString(wantSignerHex)
	if !bytes.Equal(signer.Hash, wantSigner) {
		t.Fatalf("signer hash = %x, want %x", signer.Hash, wantSigner)
	}

	notes, _ := hex.DecodeString(rsmParityNotesHex)
	rsm, err := CreateWithOptions(signer, notes, Options{Embed: true, Meta: rsmParityManifestMeta()})
	if err != nil {
		t.Fatalf("CreateWithOptions: %v", err)
	}

	wantRSM, _ := hex.DecodeString(wantRSMHex)
	if !bytes.Equal(rsm, wantRSM) {
		t.Fatalf("RSM byte mismatch with live Python capture:\n got %x\n want %x", rsm, wantRSM)
	}
	t.Logf("RSM byte-parity with live Python confirmed (%d bytes)", len(rsm))
}

// TestRSGByteParityWithPython asserts the plain (non-embedded) artifact
// signature matches Python's create_rsg(embed=False, meta=...) for the same
// signer, captured live from the installed RNS.Utilities.rnid.
func TestRSGByteParityWithPython(t *testing.T) {
	_, wantRSGHex, wantSignerHex := rsmParityPython(t)

	signer := rsmParitySigner(t)
	wantSigner, _ := hex.DecodeString(wantSignerHex)
	if !bytes.Equal(signer.Hash, wantSigner) {
		t.Fatalf("signer hash = %x, want %x", signer.Hash, wantSigner)
	}

	artifact, _ := hex.DecodeString(rsmParityArtifactHex)
	artifactMeta := msgpack.OrderedMap{
		{Key: "timestamp", Value: uint64(rsmParityReleaseTime)},
	}
	rsgBlob, err := CreateWithOptions(signer, artifact, Options{Embed: false, Meta: artifactMeta})
	if err != nil {
		t.Fatalf("CreateWithOptions: %v", err)
	}

	wantRSG, _ := hex.DecodeString(wantRSGHex)
	if !bytes.Equal(rsgBlob, wantRSG) {
		t.Fatalf("RSG byte mismatch with live Python capture:\n got %x\n want %x", rsgBlob, wantRSG)
	}
	t.Logf("RSG byte-parity with live Python confirmed (%d bytes)", len(rsgBlob))
}
