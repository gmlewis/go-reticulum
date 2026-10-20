// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestDestinationNameHelpersPythonParity drives Python's own
// Destination.app_and_aspects_from_name and Destination.hash for a
// full-name battery and compares both the split and the resulting destination
// hash (RNS/Destination.py:133-148). The Go test owns the inputs; Python owns
// the expected values, so a divergence in expand_name or the hash material is
// caught rather than asserted from a transcription.
func TestDestinationNameHelpersPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	type nameCase struct {
		Name     string `json:"name"`
		Identity string `json:"identity"`
	}
	identityHash := identityHashForTest(0x01)
	cases := []nameCase{
		{Name: "nomadnetwork.node", Identity: identityHash},
		{Name: "lxmf.delivery", Identity: identityHash},
		{Name: "lxmf.propagation", Identity: identityHash},
		{Name: "app", Identity: identityHash},
		{Name: "a.b.c.d", Identity: identityHash},
		{Name: ".leading", Identity: identityHash},
		{Name: "trailing.", Identity: identityHash},
	}
	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}

	const script = `
import sys, json
import RNS
cases = json.loads(sys.argv[1])
out = []
for c in cases:
    app, aspects = RNS.Destination.app_and_aspects_from_name(c["name"])
    h = RNS.Destination.hash(bytes.fromhex(c["identity"]), app, *aspects)
    out.append({"app": app, "aspects": aspects, "hash": h.hex()})
json.dump(out, sys.stdout)
`
	var want []struct {
		App     string   `json:"app"`
		Aspects []string `json:"aspects"`
		Hash    string   `json:"hash"`
	}
	if err := json.Unmarshal([]byte(testutils.RunPython(t, script, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}

	id := &Identity{Hash: mustHex(t, identityHash)}
	id.HexHash = identityHash
	for i, c := range cases {
		app, aspects := AppAndAspectsFromName(c.Name)
		if app != want[i].App {
			t.Errorf("AppAndAspectsFromName(%q) app = %q, want %q", c.Name, app, want[i].App)
		}
		if len(aspects) != len(want[i].Aspects) {
			t.Fatalf("AppAndAspectsFromName(%q) aspects = %v, want %v", c.Name, aspects, want[i].Aspects)
		}
		for j := range aspects {
			if aspects[j] != want[i].Aspects[j] {
				t.Errorf("AppAndAspectsFromName(%q) aspects = %v, want %v", c.Name, aspects, want[i].Aspects)
			}
		}
		if got := hex.EncodeToString(HashFromNameAndIdentity(c.Name, id)); got != want[i].Hash {
			t.Errorf("HashFromNameAndIdentity(%q) = %v, want %v (Python)", c.Name, got, want[i].Hash)
		}
		if got := hex.EncodeToString(CalculateHash(id, want[i].App, want[i].Aspects...)); got != want[i].Hash {
			t.Errorf("CalculateHash(%q) = %v, want %v (Python)", c.Name, got, want[i].Hash)
		}
	}
}

// identityHashForTest returns a deterministic 16-byte identity hash hex.
func identityHashForTest(seed byte) string {
	h := make([]byte, TruncatedHashLength/8)
	for i := range h {
		h[i] = seed + byte(i)
	}
	return hex.EncodeToString(h)
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return b
}
