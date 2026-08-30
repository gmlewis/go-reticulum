package interfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"
)

// These tests pin the Go interface hashes to Python's Interface.get_hash =
// full_hash(str(self)) for the interface types the TCP fix (tcp-hash_test.go)
// did not cover. The same class of bug applies everywhere: Go previously
// hashed "Type[Name]" for UDP, Local and Backbone interfaces, so a
// destination_table written by Python nomadnet (storing the hash in field
// [6]) could never have its receiving interface resolved by Go's
// findInterfaceByHash, leaving PathEntry.Interface nil and the path
// unusable.

// pythonFullHash returns Python RNS.Identity.full_hash of a string (SHA-256),
// or "" if the Reticulum python module is unavailable.
func pythonFullHash(t *testing.T, s string) string {
	t.Helper()
	out, err := exec.Command("python3", "-c",
		`import RNS; print(RNS.Identity.full_hash(b'`+s+`').hex())`).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// expectHashString asserts HashString equals the Python __str__ expansion and
// that its full_hash (SHA-256) agrees with a live python3 computation when the
// Reticulum module is importable.
func expectHashString(t *testing.T, got, wantStr string) {
	t.Helper()
	if got != wantStr {
		t.Fatalf("HashString = %q, want %q", got, wantStr)
	}
	if exec.Command("python3", "-c", "import RNS").Run() != nil {
		t.Log("python3 RNS not available; skipping cross-impl full_hash check")
		return
	}
	wantFull := sha256.Sum256([]byte(wantStr))
	out, err := exec.Command("python3", "-c",
		`import RNS; print(RNS.Identity.full_hash(b'`+wantStr+`').hex())`).Output()
	if err != nil {
		t.Fatalf("python3 full_hash: %v", err)
	}
	if py := strings.TrimSpace(string(out)); py != hex.EncodeToString(wantFull[:]) {
		t.Fatalf("Python full_hash(%q) = %s, want %s", wantStr, py, hex.EncodeToString(wantFull[:]))
	}
}

// fullHashHex computes SHA-256 like Python's full_hash (Identity.full_hash),
// for asserting against hashes actually stored in a Python-written
// destination_table.
func fullHashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestUDPInterfaceHashMatchesPython verifies the UDP __str__ parity:
// "UDPInterface[name/bind_ip:bind_port]" (UDPInterface.py:131-132) — the
// bound listen address, with no IPv6 bracketing.
func TestUDPInterfaceHashMatchesPython(t *testing.T) {
	t.Parallel()

	ui, err := NewUDPInterface("udp-test", "127.0.0.1", 4242, "255.255.255.255", 4242, nil)
	if err != nil {
		t.Fatalf("NewUDPInterface: %v", err)
	}
	defer func() { _ = ui.Detach() }()

	expectHashString(t, ui.HashString(), "UDPInterface[udp-test/127.0.0.1:4242]")
}

// TestLocalInterfaceHashMatchesPython verifies the shared-instance client
// __str__ parity (LocalInterface.py:372-374): the NUL-stripped AF_UNIX socket
// path when set, else the TCP target port.
func TestLocalInterfaceHashMatchesPython(t *testing.T) {
	t.Parallel()

	lciPath := &LocalClientInterface{
		BaseInterface: NewBaseInterface("Shared Instance", ModeFull, LocalBitrate),
		path:          "/home/user/.reticulum/socketfile\000extra",
		port:          4242,
	}
	expectHashString(t, lciPath.HashString(), "LocalInterface[/home/user/.reticulum/socketfileextra]")

	lciPort := &LocalClientInterface{
		BaseInterface: NewBaseInterface("Shared Instance", ModeFull, LocalBitrate),
		port:          4242,
	}
	expectHashString(t, lciPort.HashString(), "LocalInterface[4242]")
}

// TestLocalServerInterfaceHashMatchesPython verifies the server-side __str__
// parity (LocalInterface.py:495-497): "Shared Instance[...]" with the
// NUL-stripped socket path or bind port.
func TestLocalServerInterfaceHashMatchesPython(t *testing.T) {
	t.Parallel()

	lsiPath := &LocalServerInterface{
		BaseInterface: NewBaseInterface("Shared Instance", ModeFull, LocalBitrate),
		path:          "\000rns/socketfile",
		port:          4242,
	}
	expectHashString(t, lsiPath.HashString(), "Shared Instance[rns/socketfile]")

	lsiPort := &LocalServerInterface{
		BaseInterface: NewBaseInterface("Shared Instance", ModeFull, LocalBitrate),
		port:          4242,
	}
	expectHashString(t, lsiPort.HashString(), "Shared Instance[4242]")
}

// TestBackboneServerInterfaceHashMatchesPython verifies the Backbone server
// __str__ parity (BackboneInterface.py:560-563): the "BackboneInterface"
// prefix over name/bind_ip:bind_port with IPv6 bracketing — NOT the embedded
// TCPServerInterface's "TCPServerInterface[...]" string.
func TestBackboneServerInterfaceHashMatchesPython(t *testing.T) {
	t.Parallel()

	b := &BackboneInterface{TCPServerInterface: &TCPServerInterface{
		BaseInterface: NewBaseInterface("Backbone Test", ModeFull, TCPBitrateGuess),
		bindIP:        "0.0.0.0",
		bindPort:      4242,
	}}
	expectHashString(t, b.HashString(), "BackboneInterface[Backbone Test/0.0.0.0:4242]")

	v6 := &BackboneInterface{TCPServerInterface: &TCPServerInterface{
		BaseInterface: NewBaseInterface("v6", ModeFull, TCPBitrateGuess),
		bindIP:        "2001:db8::1",
		bindPort:      4242,
	}}
	expectHashString(t, v6.HashString(), "BackboneInterface[v6/[2001:db8::1]:4242]")
}

// TestBackboneClientInterfaceHashMatchesPython verifies the Backbone client
// __str__ parity (BackboneInterface.py:869-873): the prefix is
// "BackboneInterface" (not "BackboneClientInterface" or "TCPInterface"), and
// a server-spawned client uses the peer address.
func TestBackboneClientInterfaceHashMatchesPython(t *testing.T) {
	t.Parallel()

	bc := &BackboneClientInterface{TCPClientInterface: &TCPClientInterface{
		BaseInterface: NewBaseInterface("Backbone Test", ModeFull, TCPBitrateGuess),
		targetHost:    "198.51.100.7",
		targetPort:    4242,
	}}
	expectHashString(t, bc.HashString(), "BackboneInterface[Backbone Test/198.51.100.7:4242]")

	spawned := &BackboneClientInterface{TCPClientInterface: &TCPClientInterface{
		BaseInterface: NewBaseInterface("Backbone Test", ModeFull, TCPBitrateGuess),
		spawned:       true,
		remoteIP:      "198.51.100.7",
		remotePort:    51234,
	}}
	expectHashString(t, spawned.HashString(), "BackboneInterface[Backbone Test/198.51.100.7:51234]")
}

// TestBackboneStoredParityHashCrossChecks pins a hash value exactly as it
// would appear in a Python-written destination_table (field [6]) using the
// known Python full_hash math (SHA-256 of the __str__).
func TestBackboneStoredParityHashCrossChecks(t *testing.T) {
	t.Parallel()

	b := &BackboneClientInterface{TCPClientInterface: &TCPClientInterface{
		BaseInterface: NewBaseInterface("g00n Cloud Backbone", ModeFull, TCPBitrateGuess),
		targetHost:    "dfw.us.g00n.cloud",
		targetPort:    4242,
	}}
	const wantStr = "BackboneInterface[g00n Cloud Backbone/dfw.us.g00n.cloud:4242]"
	if got := b.HashString(); got != wantStr {
		t.Fatalf("HashString = %q, want %q", got, wantStr)
	}
	// The hash Go stores in destination_table field [6] must be full_hash of
	// the same string Python would store, so the table round-trips between
	// implementations.
	if got := b.HashString(); fullHashHex(got) != fullHashHex(wantStr) {
		t.Fatalf("hash material mismatch: %q vs %q", got, wantStr)
	}
	if py := pythonFullHash(t, wantStr); py != "" && py != fullHashHex(wantStr) {
		t.Fatalf("python3 full_hash(%q) = %s, want %s", wantStr, py, fullHashHex(wantStr))
	}
}
