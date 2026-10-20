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

// TestRatchetIDPythonParity derives Python Identity._get_ratchet_id's answer
// freshly (full SHA-256 of the public key, truncated to NAME_HASH_LENGTH//8;
// RNS/Identity.py:396-397) for a battery of public keys and compares RatchetID.
func TestRatchetIDPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	pubs := [][]byte{
		bytes.Repeat([]byte{0x01}, 32),
		bytes.Repeat([]byte{0xff}, 32),
		bytes.Repeat([]byte{0xab}, 16),
		{},
	}
	hexPubs := make([]string, len(pubs))
	for i, p := range pubs {
		hexPubs[i] = hex.EncodeToString(p)
	}
	in, err := json.Marshal(hexPubs)
	if err != nil {
		t.Fatal(err)
	}

	const script = `
import sys, json
import RNS
out = [RNS.Identity._get_ratchet_id(bytes.fromhex(p)).hex() for p in json.loads(sys.argv[1])]
json.dump(out, sys.stdout)
`
	var want []string
	if err := json.Unmarshal([]byte(testutils.RunPython(t, script, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	for i, p := range pubs {
		if got := hex.EncodeToString(RatchetID(p)); got != want[i] {
			t.Errorf("RatchetID(%x) = %v, want %v (Python)", p, got, want[i])
		}
	}
}

// TestTransportCurrentRatchetID pins TransportSystem.CurrentRatchetID against
// Python Identity.current_ratchet_id (RNS/Identity.py:382-393): nil when no
// ratchet is known, and the ratchet ID (not the public key) once one is.
func TestTransportCurrentRatchetID(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	dest := bytes.Repeat([]byte{0x11}, 16)
	pub := bytes.Repeat([]byte{0x22}, 32)

	if got := ts.CurrentRatchetID(dest); got != nil {
		t.Fatalf("CurrentRatchetID(unknown) = %x, want nil", got)
	}
	ts.SetRatchet(dest, pub)
	got := ts.CurrentRatchetID(dest)
	if want := RatchetID(pub); !bytes.Equal(got, want) {
		t.Errorf("CurrentRatchetID = %x, want the ratchet ID %x (not the public key %x)",
			got, want, hex.EncodeToString(pub))
	}
}
