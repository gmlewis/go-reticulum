// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rsg

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// TestRSMByteParityWithPython asserts that rsg.CreateWithOptions produces
// a byte-identical RSM (embedded manifest) and RSG (plain artifact
// signature) to Python's create_rsg for the same signer private key and
// the same meta key insertion order. The golden hex was captured from
// /tmp/rsm_parity_capture.py using RNS.Utilities.rnid.create_rsg.
//
// Ed25519 signing is deterministic, so byte-identity depends entirely on
// the msgpack envelope key order matching Python's dict insertion order:
// hashtype, hash, meta(signer, pubkey, ...user meta...), [message].
func TestRSMByteParityWithPython(t *testing.T) {
	// Deterministic private key captured from the Python script.
	prvHex := "c8699e62f1d5354063c63c3c7165b1d6f3c0dd40d51a728be403635c3bef3a6cb6d658a728a5eccc2a6b69d15787cca20260782442a3f1c408d880a507f23e80"
	prv, err := hex.DecodeString(prvHex)
	if err != nil {
		t.Fatalf("decode prv: %v", err)
	}
	signer, err := rns.FromBytes(prv, nil)
	if err != nil {
		t.Fatalf("FromBytes: %v", err)
	}

	// Sanity: signer hash matches the Python capture.
	wantSignerHash, _ := hex.DecodeString("a60aa27f1ffa1f59f39d55a85ca62490")
	if !bytes.Equal(signer.Hash, wantSignerHash) {
		t.Fatalf("signer hash = %x, want %x", signer.Hash, wantSignerHash)
	}

	notes, _ := hex.DecodeString("52656c65617365206e6f74657320666f7220312e302e300a496e697469616c2072656c656173652e")
	releaseTime := int64(1718000000)
	origin, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	commit := "abc123def4567890abc123def4567890abc123de"

	// manifest_meta keys IN THIS ORDER (Python dict insertion order):
	// name, version, released, timestamp, origin, path, commit, artifacts.
	manifestMeta := msgpack.OrderedMap{
		{Key: "name", Value: "pkg"},
		{Key: "version", Value: "1.0.0"},
		{Key: "released", Value: "2024-06-10T06:13:20Z"},
		{Key: "timestamp", Value: uint64(releaseTime)},
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

	rsm, err := CreateWithOptions(signer, notes, Options{Embed: true, Meta: manifestMeta})
	if err != nil {
		t.Fatalf("CreateWithOptions: %v", err)
	}

	wantRSM, _ := hex.DecodeString("bd124cd50e25a78ee990744d28cf2455631a371977177e5ed486cf9b4484addcfa43b38db3ccbc159e5e5b40c187b0a328c5ac2342f5742128daaa48ec5d5e0e84a86861736874797065a6736861323536a468617368c420ebe1eaf80a2601c888a23bc1dcda699730c83016cea1d85e8dcbd090c63d4995a46d6574618aa67369676e6572c410a60aa27f1ffa1f59f39d55a85ca62490a67075626b6579c4407ff39021a400f5407f9eea33c2f06a2b635ce9699acd26817eac6439608a051bf28419f7d1580fc7b29558325080f28a9323ed976921a740367ddd655ceebd23a46e616d65a3706b67a776657273696f6ea5312e302e30a872656c6561736564b4323032342d30362d31305430363a31333a32305aa974696d657374616d70ce66669980a66f726967696ec41000112233445566778899aabbccddeeffa470617468b16d61696e2f746573747265706f2e676974a6636f6d6d6974d92861626331323364656634353637383930616263313233646566343536373839306162633132336465a96172746966616374739182a46e616d65aa62696e6172792e62696ea3727367c404deadbeefa76d657373616765c42852656c65617365206e6f74657320666f7220312e302e300a496e697469616c2072656c656173652e")
	if !bytes.Equal(rsm, wantRSM) {
		t.Fatalf("RSM byte mismatch:\n got %x\n want %x", rsm, wantRSM)
	}
	t.Logf("RSM byte-parity with Python confirmed (%d bytes)", len(rsm))
}

// TestRSGByteParityWithPython asserts the plain (non-embedded) artifact
// signature matches Python's create_rsg(embed=False, meta=...) for the
// same signer and meta insertion order.
func TestRSGByteParityWithPython(t *testing.T) {
	prvHex := "c8699e62f1d5354063c63c3c7165b1d6f3c0dd40d51a728be403635c3bef3a6cb6d658a728a5eccc2a6b69d15787cca20260782442a3f1c408d880a507f23e80"
	prv, err := hex.DecodeString(prvHex)
	if err != nil {
		t.Fatalf("decode prv: %v", err)
	}
	signer, err := rns.FromBytes(prv, nil)
	if err != nil {
		t.Fatalf("FromBytes: %v", err)
	}

	artifact, _ := hex.DecodeString("61727469666163742062797465732068657265")
	artifactMeta := msgpack.OrderedMap{
		{Key: "timestamp", Value: uint64(1718000000)},
	}
	rsgBlob, err := CreateWithOptions(signer, artifact, Options{Embed: false, Meta: artifactMeta})
	if err != nil {
		t.Fatalf("CreateWithOptions: %v", err)
	}

	wantRSG, _ := hex.DecodeString("74f53108d6118f75d844b4b7e4f6be99c5e8ac2cf4aede4502528e862307fe293db2e5e7c63ff0ada6e4bc390e01e6d526b0b6e604fff8a84e1769e4e4c4330f83a86861736874797065a6736861323536a468617368c4209561157b234727702989f05cd27785498d01170f1e1f95f836c585857a40ffd2a46d65746183a67369676e6572c410a60aa27f1ffa1f59f39d55a85ca62490a67075626b6579c4407ff39021a400f5407f9eea33c2f06a2b635ce9699acd26817eac6439608a051bf28419f7d1580fc7b29558325080f28a9323ed976921a740367ddd655ceebd23a974696d657374616d70ce66669980")
	if !bytes.Equal(rsgBlob, wantRSG) {
		t.Fatalf("RSG byte mismatch:\n got %x\n want %x", rsgBlob, wantRSG)
	}
	t.Logf("RSG byte-parity with Python confirmed (%d bytes)", len(rsgBlob))
}
