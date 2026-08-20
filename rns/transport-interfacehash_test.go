// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
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
	// Verified LIVE against Python's RNS.Identity.full_hash(str(self)) rather
	// than asserted as a bare literal — this is the dispatch that lets Go
	// resolve interfaces in a Python-written destination_table.
	testutils.SkipIfNoPythonRNS(t)
	wantHex := pythonFullHashHex(t, iface.hashStr)
	if h := bytesToHex(got); h != wantHex {
		t.Fatalf("interfaceHash = %s, want Python full_hash(%q) = %s", h, iface.hashStr, wantHex)
	}
}

// pythonFullHashHex execs `python3 -c "import RNS; print(RNS.Identity.full_hash(<s>).hex())"`
// and returns the hex digest, so interface-hash parity is proven against the real Python
// implementation rather than a hand-typed constant.
func pythonFullHashHex(t *testing.T, s string) string {
	t.Helper()
	out, err := exec.Command("python3", "-c",
		`import RNS; print(RNS.Identity.full_hash(`+goBytesLiteral(s)+`).hex())`,
	).Output()
	if err != nil {
		t.Fatalf("python3 full_hash(%q): %v", s, err)
	}
	return strings.TrimSpace(string(out))
}

// goBytesLiteral renders a Go string as a Python b"..." literal that safely
// represents the same bytes (no escaping beyond what b-strings require).
func goBytesLiteral(s string) string {
	var b strings.Builder
	b.WriteString(`b"`)
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(`"`)
	return b.String()
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
