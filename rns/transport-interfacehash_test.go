// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
)

// hashStringIface wraps capturingInterface to add an optional HashString
// method, so interfaceHash's HashString-dispatch can be exercised without
// standing up a real networked interface.
type hashStringIface struct {
	*capturingInterface
	hashStr string
}

func (h *hashStringIface) HashString() string { return h.hashStr }

// TestInterfaceHashPrefersHashString verifies interfaceHash hashes the
// HashString() string when present (matching Python's full_hash(str(self)))
// rather than the Type[Name] default. This is the dispatch that lets Go
// resolve interfaces in a Python-written destination_table.
func TestInterfaceHashPrefersHashString(t *testing.T) {
	t.Parallel()
	base := &capturingInterface{name: "g00n Cloud Dallas"}
	iface := &hashStringIface{
		capturingInterface: base,
		hashStr:            "TCPInterface[g00n Cloud Dallas/dfw.us.g00n.cloud:6969]",
	}

	got := interfaceHash(iface)
	want := FullHash([]byte(iface.hashStr))
	if !bytes.Equal(got, want) {
		t.Fatalf("interfaceHash with HashString = %x, want full_hash(%q) = %x", got, iface.hashStr, want)
	}

	// And it must NOT equal the old Type[Name] default for this interface.
	oldDefault := FullHash([]byte("capture[g00n Cloud Dallas]"))
	if bytes.Equal(got, oldDefault) {
		t.Fatalf("interfaceHash fell back to Type[Name] default %x; expected HashString to take precedence", got)
	}

	// The known Python-stored hash for the user's retibooks path (proven).
	const wantHex = "e7e198b4aa347e50c20d1f51520d64e03ebb141b965f168d57e73be2d3b73ef5"
	if h := bytesToHex(got); h != wantHex {
		t.Fatalf("interfaceHash = %s, want %s", h, wantHex)
	}
}

// TestInterfaceHashDefaultTypeNam verifies an interface WITHOUT HashString
// still hashes "Type[Name]" (the Python parity default for AutoInterface,
// I2P, KISS, RNode, Serial, Pipe, etc., whose __str__ is f"{Type}[{name}]").
func TestInterfaceHashDefaultTypeNam(t *testing.T) {
	t.Parallel()
	iface := &capturingInterface{name: "Default Interface"}
	iface.Type() // "capture"

	got := interfaceHash(iface)
	want := FullHash([]byte("capture[Default Interface]"))
	if !bytes.Equal(got, want) {
		t.Fatalf("default interfaceHash = %x, want full_hash(\"capture[Default Interface]\") = %x", got, want)
	}
}

func bytesToHex(b []byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexd[v>>4]
		out[i*2+1] = hexd[v&0x0f]
	}
	return string(out)
}