// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// Cross-implementation (Go <-> Python RNS) round-trip tests for every on-disk
// RNS storage format both implementations write to the shared
// ~/.reticulum/storage/ tree. These lock in the on-disk msgpack contract so a
// future change cannot silently break interoperability (see the
// known_destinations str-vs-bin map-key regression that made Python RNS throw
// InvalidStringException on Go-written files).
//
// Each test runs only when `python3` + RNS are importable (SkipIfNoPythonRNS),
// so the suite stays green on machines without the Python side.

// hexHash returns the lowercase hex of a hash, for building Python scripts.
func hexHash(b []byte) string { return hex.EncodeToString(b) }

// TestStorageInteropKnownDestinations verifies the format that triggered the
// original rnpath InvalidStringException: Go must emit msgpack BIN map keys
// (raw destination_hash bytes), matching Python, not str keys. Both directions.
func TestStorageInteropKnownDestinations(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	// 0x87 is exactly the byte Python choked on in the rnpath log; use it as
	// the first key byte to prove the fix end-to-end.
	destHash := bytes.Repeat([]byte{0x87}, 16)
	packetHash := bytes.Repeat([]byte{0x11}, 16)
	publicKey := bytes.Repeat([]byte{0x22}, 32)
	appData := []byte("app-data")

	// --- Go writes, Python reads (the direction that was broken) ---
	dir := testutils.TempDir(t, tempDirPrefix)
	ts := NewTransportSystem(nil)
	ts.Remember(packetHash, destHash, publicKey, appData)
	ts.SaveKnownDestinations(dir)

	out := testutils.RunPython(t, knownDestReadScript, filepath.Join(dir, "known_destinations"))
	if !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("python failed to read Go-written known_destinations:\n%s", out)
	}

	// --- Python writes (bin keys), Go reads ---
	dir2 := testutils.TempDir(t, tempDirPrefix)
	pyDest := bytes.Repeat([]byte{0x33}, 16)
	pyPkt := bytes.Repeat([]byte{0x44}, 16)
	pyPub := bytes.Repeat([]byte{0x55}, 32)
	writeScript := fmt.Sprintf(knownDestWriteScript, hexHash(pyDest), hexHash(pyPkt), hexHash(pyPub))
	testutils.RunPython(t, writeScript, filepath.Join(dir2, "known_destinations"))

	ts2 := NewTransportSystem(nil)
	ts2.LoadKnownDestinations(dir2)
	ts2.mu.Lock()
	got, ok := ts2.knownDestinations[string(pyDest)]
	ts2.mu.Unlock()
	if !ok {
		t.Fatal("Go failed to load Python-written bin-keyed known_destinations entry")
	}
	if len(got) < 4 {
		t.Fatalf("loaded value len = %v, want >= 4", len(got))
	}
	if pub, okB := got[2].([]byte); !okB || !bytes.Equal(pub, pyPub) {
		t.Fatalf("loaded public key = %v, want %x", got[2], pyPub)
	}
}

const knownDestReadScript = `import sys
from RNS.vendor import umsgpack
with open(sys.argv[1], "rb") as f:
    d = umsgpack.load(f)
assert isinstance(d, dict), "top-level %r is not a dict" % type(d)
for k, v in d.items():
    assert isinstance(k, bytes), "key %r is %r not bytes (str-vs-bin bug)" % (k, type(k))
    assert len(k) == 16, "key len %d want 16" % len(k)
    assert isinstance(v, list) and len(v) >= 4, "value not a >=4-list: %r" % (v,)
    assert isinstance(v[1], bytes) and isinstance(v[2], bytes), "value bytes fields wrong"
print("OK", len(d))
`

const knownDestWriteScript = `import sys, time
from RNS.vendor import umsgpack
dest = bytes.fromhex("%s")
pkt = bytes.fromhex("%s")
pub = bytes.fromhex("%s")
d = {dest: [time.time(), pkt, pub, b"app-data", 0]}
with open(sys.argv[1], "wb") as f:
    umsgpack.dump(d, f)
print("OK")
`

// TestStorageInteropRatchets verifies storage/ratchets/<hex>: str literal
// keys "ratchet"(bin value)/"received"(float), matching Python. Both
// directions through the real persistRatchet/GetRatchet paths.
func TestStorageInteropRatchets(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	dir := testutils.TempDir(t, tempDirPrefix)
	ts := NewTransportSystem(nil)
	ts.storagePath = dir
	destHash := bytes.Repeat([]byte{0x7a}, 16)
	ratchetPub := bytes.Repeat([]byte{0x5a}, 32)

	// --- Go writes, Python reads ---
	ts.persistRatchet(dir, destHash, ratchetPub)
	ratchetPath := filepath.Join(dir, "ratchets", hexHash(destHash))
	out := testutils.RunPython(t, ratchetReadScript, ratchetPath)
	if !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("python failed to read Go-written ratchet:\n%s", out)
	}

	// --- Python writes, Go reads ---
	dir2 := testutils.TempDir(t, tempDirPrefix)
	pyDest := bytes.Repeat([]byte{0x7b}, 16)
	pyRatchet := bytes.Repeat([]byte{0x5b}, 32)
	writeScript := fmt.Sprintf(ratchetWriteScript, hexHash(pyRatchet))
	pyPath := filepath.Join(dir2, "ratchets", hexHash(pyDest))
	if err := os.MkdirAll(filepath.Dir(pyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	testutils.RunPython(t, writeScript, pyPath)

	ts2 := NewTransportSystem(nil)
	ts2.storagePath = dir2
	got := ts2.GetRatchet(pyDest)
	if !bytes.Equal(got, pyRatchet) {
		t.Fatalf("Go GetRatchet = %x, want %x (Python-written ratchet not loaded)", got, pyRatchet)
	}
}

const ratchetReadScript = `import sys
from RNS.vendor import umsgpack
with open(sys.argv[1], "rb") as f:
    d = umsgpack.unpackb(f.read())
assert isinstance(d, dict), "not a dict: %r" % type(d)
assert set(d.keys()) == {"ratchet", "received"}, "keys %r" % list(d.keys())
assert isinstance(d["ratchet"], bytes), "ratchet value not bytes: %r" % type(d["ratchet"])
assert isinstance(d["received"], float), "received not float: %r" % type(d["received"])
print("OK")
`

const ratchetWriteScript = `import sys, time
from RNS.vendor import umsgpack
d = {"ratchet": bytes.fromhex("%s"), "received": time.time()}
with open(sys.argv[1], "wb") as f:
    f.write(umsgpack.packb(d))
print("OK")
`

// TestStorageInteropBlackhole verifies storage/blackhole/<file>: BIN identity
// hash map keys with str-keyed {source, until, reason} sub-maps (the one
// format Go already encodes correctly). Both directions: Go packBlackholeList
// -> Python, and a Python-written "local" file -> Go ReloadBlackhole.
func TestStorageInteropBlackhole(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	identityHash := bytes.Repeat([]byte{0xb1}, 16)
	source := bytes.Repeat([]byte{0x5c}, 16)

	// --- Go writes, Python reads ---
	entries := []blackholeListEntry{
		{identityHash: copyBytes(identityHash), source: copyBytes(source), until: nil, reason: "test"},
	}
	packed, err := packBlackholeList(entries)
	if err != nil {
		t.Fatal(err)
	}
	dir := testutils.TempDir(t, tempDirPrefix)
	path := filepath.Join(dir, "local")
	if err := os.WriteFile(path, packed, 0o600); err != nil {
		t.Fatal(err)
	}
	out := testutils.RunPython(t, blackholeReadScript, path)
	if !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("python failed to read Go-written blackhole list:\n%s", out)
	}

	// --- Python writes (bin keys), Go reads via ReloadBlackhole ---
	dir2 := testutils.TempDir(t, tempDirPrefix)
	blackholeDir := filepath.Join(dir2, "blackhole")
	if err := os.MkdirAll(blackholeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pyIdentity := bytes.Repeat([]byte{0xb2}, 16)
	pySource := bytes.Repeat([]byte{0x5d}, 16)
	writeScript := fmt.Sprintf(blackholeWriteScript, hexHash(pyIdentity), hexHash(pySource))
	testutils.RunPython(t, writeScript, filepath.Join(blackholeDir, "local"))

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.blackholePath = blackholeDir
	ts.ReloadBlackhole()
	ts.mu.Lock()
	entry, ok := ts.blackholedIdentities[string(pyIdentity)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("Go failed to load Python-written bin-keyed blackhole entry")
	}
	if entry.Reason != "py-test" {
		t.Fatalf("loaded reason = %q, want \"py-test\"", entry.Reason)
	}
}

const blackholeReadScript = `import sys
from RNS.vendor import umsgpack
with open(sys.argv[1], "rb") as f:
    d = umsgpack.unpackb(f.read())
assert isinstance(d, dict) and len(d) == 1, "want 1-entry dict, got %r" % d
for k, v in d.items():
    assert isinstance(k, bytes) and len(k) == 16, "outer key not 16-byte bin: %r" % (k,)
    assert isinstance(v, dict), "value not dict: %r" % type(v)
    assert set(v.keys()) == {"source", "until", "reason"}, "inner keys %r" % list(v.keys())
    assert isinstance(v["source"], bytes), "source not bytes"
    assert v["reason"] == "test" or v["reason"] is None, "reason %r" % v["reason"]
print("OK")
`

const blackholeWriteScript = `import sys
from RNS.vendor import umsgpack
ident = bytes.fromhex("%s")
src = bytes.fromhex("%s")
d = {ident: {"source": src, "until": None, "reason": "py-test"}}
with open(sys.argv[1], "wb") as f:
    f.write(umsgpack.packb(d))
print("OK")
`

// TestStorageInteropTransportIdentity verifies the raw 64-byte transport
// identity file (X25519 private || Ed25519 private) round-trips byte-identical
// between Go ToFile/FromFile and Python Identity.to_file/from_file.
func TestStorageInteropTransportIdentity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	id := mustTestNewIdentity(t, true)
	goPriv := id.GetPrivateKey()
	if len(goPriv) != 64 {
		t.Fatalf("Go private key len = %v, want 64", len(goPriv))
	}

	// --- Go writes, Python reads (byte-identical) ---
	dir := testutils.TempDir(t, tempDirPrefix)
	goPath := filepath.Join(dir, "transport_identity")
	if err := id.ToFile(goPath); err != nil {
		t.Fatal(err)
	}
	out := testutils.RunPython(t, fmt.Sprintf(identityReadScript, hexHash(goPriv)), goPath)
	if !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("python failed to read Go-written identity:\n%s", out)
	}

	// --- Python writes a real identity, Go reads it ---
	dir2 := testutils.TempDir(t, tempDirPrefix)
	pyPath := filepath.Join(dir2, "transport_identity")
	out = strings.TrimSpace(testutils.RunPython(t, identityWriteScript, pyPath))
	loaded, err := FromFile(pyPath, nil)
	if err != nil {
		t.Fatalf("Go FromFile of Python-written identity: %v", err)
	}
	wantPub, err := hex.DecodeString(out)
	if err != nil {
		t.Fatalf("python public key hex %q: %v", out, err)
	}
	if !bytes.Equal(loaded.GetPublicKey(), wantPub) {
		t.Fatalf("loaded public key = %x, want %x (Python identity round-trip)", loaded.GetPublicKey(), wantPub)
	}
}

const identityReadScript = `import sys
from RNS.vendor import umsgpack
want = bytes.fromhex("%s")
with open(sys.argv[1], "rb") as f:
    data = f.read()
assert len(data) == 64, "identity file len %%d want 64" %% len(data)
assert data == want, "identity bytes mismatch"
print("OK")
`

const identityWriteScript = `import sys
import RNS
idn = RNS.Identity(create_keys=True)
idn.to_file(sys.argv[1])
print(idn.get_public_key().hex())
`

// TestStorageInteropDestinationTable verifies the destination_table format is
// now Python-compatible BOTH directions through the real persist/load paths:
//
//   - Go writes (via persistPathTable) the Python layout
//     [destHash, timestamp(float s), next_hop, hops, expires(float s),
//     random_blobs, interface_hash, packet_hash] AND the accompanying
//     cache/announces/<hex(packet_hash)> file ([raw, "Type[Name]"]). Python
//     asserts the exact layout + that get_cached_packet-style lookup recovers
//     the raw announce.
//   - Python writes the same layout + cache file; Go loads it via
//     LoadPathTable and the entry is reconstructed (Packet from the cache,
//     Interface reattached by interface_hash, float-second timestamps).
//
// See memory destination-table-timestamp-unit-mismatch for the history: this
// was previously a structural-only test because the layouts diverged; the
// transport.go pathTablePersist/load rewrite made them match.
func TestStorageInteropDestinationTable(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	// --- Go writes, Python reads ---
	dir := testutils.TempDir(t, tempDirPrefix)
	ts := NewTransportSystem(nil)
	ts.storagePath = dir
	pipe := interfaces.NewPipeInterface("interoppipe", func(_ []byte, _ interfaces.Interface) {})
	destHash := bytes.Repeat([]byte{0xd7}, 16)
	nextHop := bytes.Repeat([]byte{0xe1}, 16)
	pktHash := bytes.Repeat([]byte{0x0b}, 32)
	rawAnnounce := []byte("fake-announce-raw-bytes")
	ts.mu.Lock()
	ts.pathTable[string(destHash)] = &PathEntry{
		Timestamp:   time.Now(),
		NextHop:     nextHop,
		Hops:        3,
		Expires:     time.Now().Add(time.Hour),
		RandomBlobs: [][]byte{bytes.Repeat([]byte{0x99}, 8)},
		Interface:   pipe,
		IfaceHash:   interfaceHash(pipe),
		Packet:      rawAnnounce,
		PacketHash:  pktHash,
	}
	ts.mu.Unlock()
	ts.persistPathTable()
	path := filepath.Join(dir, "destination_table")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persistPathTable did not write: %v", err)
	}
	out := testutils.RunPython(t, destTableReadScript, path)
	if !strings.HasPrefix(strings.TrimSpace(out), "OK") {
		t.Fatalf("python failed to read Go-written destination_table:\n%s", out)
	}

	// --- Python writes, Go reads ---
	dir2 := testutils.TempDir(t, tempDirPrefix)
	ts2 := NewTransportSystem(nil)
	ts2.storagePath = dir2
	// Register a pipe with the SAME name so Go's findInterfaceByHash reattaches
	// the live interface by its interface_hash (SHA-256 of "PipeInterface[interoppipe]").
	pipe2 := interfaces.NewPipeInterface("interoppipe", func(_ []byte, _ interfaces.Interface) {})
	ts2.RegisterInterface(pipe2)

	destHash2 := bytes.Repeat([]byte{0xc7}, 16)
	nextHop2 := bytes.Repeat([]byte{0xf1}, 16)
	pktHash2 := bytes.Repeat([]byte{0x1b}, 32)
	writeScript := fmt.Sprintf(destTableWriteScript,
		hexHash(destHash2), hexHash(nextHop2), hexHash(pktHash2), hexHash(interfaceHash(pipe2)))
	testutils.RunPython(t, writeScript, filepath.Join(dir2, "destination_table"))

	ts2.LoadPathTable()
	ts2.mu.Lock()
	entry, ok := ts2.pathTable[string(destHash2)]
	ts2.mu.Unlock()
	if !ok {
		t.Fatal("Go failed to load Python-written destination_table entry")
	}
	if entry.Hops != 3 {
		t.Fatalf("loaded hops = %v, want 3", entry.Hops)
	}
	if !bytes.Equal(entry.NextHop, nextHop2) {
		t.Fatalf("loaded nextHop = %x, want %x", entry.NextHop, nextHop2)
	}
	wantRaw := []byte("py-announce-raw")
	if !bytes.Equal(entry.Packet, wantRaw) {
		t.Fatalf("Packet not reconstructed from cache/announces: %v, want %v", entry.Packet, wantRaw)
	}
	if !bytes.Equal(entry.IfaceHash, interfaceHash(pipe2)) {
		t.Fatalf("loaded IfaceHash = %x, want %x", entry.IfaceHash, interfaceHash(pipe2))
	}
	if entry.Interface != pipe2 {
		t.Fatalf("Interface not reattached by hash: got %v, want pipe2", entry.Interface)
	}
	if entry.Expires.IsZero() || entry.Timestamp.IsZero() {
		t.Fatalf("timestamps not decoded: ts=%v exp=%v", entry.Timestamp, entry.Expires)
	}
}

const destTableReadScript = `import sys, os
from RNS.vendor import umsgpack
dt = sys.argv[1]
# cache/announces is the sibling of the storage dir (dirname of storage dir).
cache_dir = os.path.join(os.path.dirname(os.path.dirname(dt)), "cache", "announces")
with open(dt, "rb") as f:
    lst = umsgpack.unpackb(f.read())
assert isinstance(lst, list) and len(lst) == 1, "want 1-entry list, got %r" % type(lst)
e = lst[0]
assert isinstance(e, list) and len(e) == 8, "entry len %d want 8" % len(e)
assert isinstance(e[0], bytes) and len(e[0]) == 16, "destHash not 16-byte bin"
assert isinstance(e[1], float), "timestamp not float (Python layout): %r" % type(e[1])
assert isinstance(e[2], bytes), "next_hop not bytes"
assert isinstance(e[3], int), "hops not int: %r" % type(e[3])
assert isinstance(e[4], float), "expires not float: %r" % type(e[4])
assert isinstance(e[5], list), "blobs not list"
assert isinstance(e[6], bytes) and len(e[6]) == 32, "iface_hash not 32-byte bin: %r" % type(e[6])
assert isinstance(e[7], bytes) and len(e[7]) == 32, "packet_hash not 32-byte bin: %r" % type(e[7])
cache_path = os.path.join(cache_dir, e[7].hex())
assert os.path.isfile(cache_path), "missing announce cache file %s" % cache_path
with open(cache_path, "rb") as f:
    c = umsgpack.unpackb(f.read())
assert isinstance(c, list) and len(c) == 2, "cache entry not 2-list: %r" % (c,)
assert c[0] == b"fake-announce-raw-bytes", "raw announce mismatch: %r" % c[0]
assert c[1] == "PipeInterface[interoppipe]", "iface ref mismatch: %r" % c[1]
print("OK")
`

// destTableWriteScript: dest hex, next_hop hex, packet_hash hex, iface_hash hex.
// Python writes the Python layout + the cache/announces/<hex(packet_hash)> file
// so Go's LoadPathTable can recover the raw announce and reattach the interface.
const destTableWriteScript = `import sys, os, time
from RNS.vendor import umsgpack
dest  = bytes.fromhex("%s")
nh    = bytes.fromhex("%s")
pkt   = bytes.fromhex("%s")
iface = bytes.fromhex("%s")
entry = [dest, time.time(), nh, 3, time.time()+3600, [b"\x99"*8], iface, pkt]
with open(sys.argv[1], "wb") as f:
    f.write(umsgpack.packb([entry]))
cache_dir = os.path.join(os.path.dirname(os.path.dirname(sys.argv[1])), "cache", "announces")
os.makedirs(cache_dir, exist_ok=True)
with open(os.path.join(cache_dir, pkt.hex()), "wb") as f:
    f.write(umsgpack.packb([b"py-announce-raw", "PipeInterface[interoppipe]"]))
print("OK")
`
