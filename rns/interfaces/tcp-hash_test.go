// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
)

// TestTCPClientInterfaceHashMatchesPython locks in the cross-implementation
// parity bug that made gonomadnet unable to use a Python-written
// destination_table: Go's interface hash must equal Python's
// Interface.get_hash, which is full_hash(str(self)). For a TCP client that is
// "TCPInterface[name/target_host:target_port]" — NOT "TCPInterface[name]" —
// so a destination_table written by Python nomadnet (storing the former hash
// in field [6]) could not have its receiving interface resolved by Go's
// findInterfaceByHash, leaving PathEntry.Interface nil.
//
// The proven case is the user's "g00n Cloud Dallas" path to the retibooks
// node: the on-disk destination_table stores iface hash
// e7e198b4aa347e50c20d1f51520d64e03ebb141b965f168d57e73be2d3b73ef5, which is
// full_hash("TCPInterface[g00n Cloud Dallas/dfw.us.g00n.cloud:6969]").
func TestTCPClientInterfaceHashMatchesPython(t *testing.T) {
	t.Parallel()
	tci := &TCPClientInterface{
		BaseInterface: NewBaseInterface("g00n Cloud Dallas", ModeFull, TCPBitrateGuess),
		targetHost:    "dfw.us.g00n.cloud",
		targetPort:    6969,
	}

	const wantStr = "TCPInterface[g00n Cloud Dallas/dfw.us.g00n.cloud:6969]"
	if got := tci.HashString(); got != wantStr {
		t.Fatalf("HashString = %q, want %q", got, wantStr)
	}
	if got := tci.Type(); got != "TCPInterface" {
		t.Fatalf("Type = %q, want TCPInterface", got)
	}

	// The hash stored in the user's Python-written destination_table.
	wantHash := "e7e198b4aa347e50c20d1f51520d64e03ebb141b965f168d57e73be2d3b73ef5"
	sum := sha256.Sum256([]byte(tci.HashString()))
	if got := hex.EncodeToString(sum[:]); got != wantHash {
		t.Fatalf("full_hash(HashString) = %s, want %s (the Python-stored iface hash)", got, wantHash)
	}

	if exec.Command("python3", "-c", "import RNS").Run() != nil {
		t.Log("python3 RNS not available; skipping cross-impl full_hash check")
		return
	}

	// Cross-check that Python's full_hash of the same __str__ string agrees.
	out, err := exec.Command("python3", "-c",
		`import RNS; print(RNS.Identity.full_hash(b"TCPInterface[g00n Cloud Dallas/dfw.us.g00n.cloud:6969]").hex())`,
	).Output()
	if err != nil {
		t.Fatalf("python3 full_hash: %v", err)
	}
	if py := strings.TrimSpace(string(out)); py != wantHash {
		t.Fatalf("Python full_hash = %s, want %s", py, wantHash)
	}
}

// TestTCPServerInterfaceHashMatchesPython verifies the server-side __str__
// parity: "TCPServerInterface[name/bind_ip:bind_port]".
func TestTCPServerInterfaceHashMatchesPython(t *testing.T) {
	t.Parallel()
	tsi := &TCPServerInterface{
		BaseInterface: NewBaseInterface("Test TCP Server", ModeFull, TCPBitrateGuess),
		bindIP:        "0.0.0.0",
		bindPort:      4242,
	}
	const wantStr = "TCPServerInterface[Test TCP Server/0.0.0.0:4242]"
	if got := tsi.HashString(); got != wantStr {
		t.Fatalf("HashString = %q, want %q", got, wantStr)
	}
}

// TestSpawnedTCPClientHashUsesPeerAddress verifies a server-spawned client's
// hash uses the peer's remote IP/port (Python TCPInterface.py:596-597),
// matching Python's __str__ for inbound-spawned interfaces.
func TestSpawnedTCPClientHashUsesPeerAddress(t *testing.T) {
	t.Parallel()
	tci := &TCPClientInterface{
		BaseInterface: NewBaseInterface("Test TCP Server", ModeFull, TCPBitrateGuess),
		spawned:       true,
		remoteIP:      "198.51.100.7",
		remotePort:    51234,
	}
	const wantStr = "TCPInterface[Test TCP Server/198.51.100.7:51234]"
	if got := tci.HashString(); got != wantStr {
		t.Fatalf("spawned HashString = %q, want %q", got, wantStr)
	}
}

// TestIPv6TargetHashBrackets verifies the IPv6-literal bracketing Python
// applies when the host contains ":".
func TestIPv6TargetHashBrackets(t *testing.T) {
	t.Parallel()
	tci := &TCPClientInterface{
		BaseInterface: NewBaseInterface("v6peer", ModeFull, TCPBitrateGuess),
		targetHost:    "2001:db8::1",
		targetPort:    4242,
	}
	const wantStr = "TCPInterface[v6peer/[2001:db8::1]:4242]"
	if got := tci.HashString(); got != wantStr {
		t.Fatalf("IPv6 HashString = %q, want %q", got, wantStr)
	}
}
