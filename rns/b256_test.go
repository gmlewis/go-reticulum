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

func TestB256RepGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", []byte{}, ""},
		{"zero", []byte{0x00}, "a"},
		{"ff", []byte{0xff}, "𐐏"},
	}
	// range(16) -> "abcdefghijklmnop"
	var rb []byte
	for i := range 16 {
		rb = append(rb, byte(i))
	}
	cases = append(cases, struct {
		name string
		in   []byte
		want string
	}{"range16", rb, "abcdefghijklmnop"})

	for _, c := range cases {
		if got := B256Rep(c.in); got != c.want {
			t.Errorf("B256Rep(%v) = %q, want %q", c.name, got, c.want)
		}
	}
}

// TestB256RepPythonParity derives Python RNS.b256rep's answer freshly over
// every byte value and a few mixed runs, so the alphabet cannot drift from the
// reference (RNS/__init__.py:516-537).
func TestB256RepPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	cases := [][]byte{
		{},
		all,
		bytes.Repeat([]byte{0xff}, 5),
		{0x00, 0x17, 0x18, 0x80},
	}
	hexCases := make([]string, len(cases))
	for i, c := range cases {
		hexCases[i] = hex.EncodeToString(c)
	}
	in, err := json.Marshal(hexCases)
	if err != nil {
		t.Fatal(err)
	}

	const script = `
import sys, json
import RNS
out = [RNS.b256rep(bytes.fromhex(h)) for h in json.loads(sys.argv[1])]
json.dump(out, sys.stdout)
`
	var want []string
	if err := json.Unmarshal([]byte(testutils.RunPython(t, script, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	for i, c := range cases {
		if got := B256Rep(c); got != want[i] {
			t.Errorf("B256Rep(%x) = %q, want %q (Python)", c, got, want[i])
		}
		if got := PrettyB256Rep(c); got != "<"+want[i]+">" {
			t.Errorf("PrettyB256Rep(%x) = %q, want %q", c, got, "<"+want[i]+">")
		}
	}
}

func TestB256RoundTrip(t *testing.T) {
	t.Parallel()
	var all []byte
	for i := range 256 {
		all = append(all, byte(i))
	}
	encoded := B256Rep(all)
	decoded, err := B256ToBytes(encoded)
	if err != nil {
		t.Fatalf("B256ToBytes round-trip failed: %v", err)
	}
	if !bytes.Equal(decoded, all) {
		t.Errorf("round-trip mismatch: got %v bytes, want %v", len(decoded), len(all))
	}
}

func TestB256ToBytesInvalid(t *testing.T) {
	t.Parallel()
	// 'w' is intentionally absent from the b256 alphabet (0x17 is 'x').
	if _, err := B256ToBytes("w"); err == nil {
		t.Fatal("B256ToBytes('w') = nil error, want error for unknown rune")
	}
}
