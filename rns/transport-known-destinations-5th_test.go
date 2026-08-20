// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// knownDestFixtureDestHashHex is the 16-byte destination hash key (hex) of the
// single fixture entry these tests round-trip, matching
// goldenKnownDestDestHash.
const knownDestFixtureDestHashHex = "aabbccddeeff00112233445566778899"

// goldenKnownDestinations4Hex is a Python-written (RNS.vendor.umsgpack) known
// destinations map with a single OLD pre-v1.3.0 4-element entry
// [timestamp, packet_hash, public_key, app_data] keyed by a 16-byte
// destination hash. Captured from the original Reticulum repo so the Go port
// migrates the exact on-disk byte layout Python would have produced.
const goldenKnownDestinations4Hex = "81c410aabbccddeeff0011223344556677889994cb41d954fc40000000c4207777777777777777777777777777777777777777777777777777777777777777c440000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3fc404deadbeef"

// goldenKnownDestinations5Hex is the same entry with the v1.3.0+ 5th
// use-timestamp element present (0 = never used). Captured from Python for
// byte-exact layout parity.
const goldenKnownDestinations5Hex = "81c410aabbccddeeff0011223344556677889995cb41d954fc40000000c4207777777777777777777777777777777777777777777777777777777777777777c440000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3fc404deadbeef00"

// goldenKnownDestDestHash is the 16-byte destination hash key of the golden
// entries above.
var goldenKnownDestDestHash = []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99}

// writeGoldenKnownDestinations writes the given hex payload to
// <storagePath>/known_destinations and returns storagePath.
func writeGoldenKnownDestinations(t *testing.T, storagePath, hexPayload string) {
	t.Helper()
	if err := os.MkdirAll(storagePath, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := hex.DecodeString(hexPayload)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "known_destinations"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// pythonWriteKnownDestinations writes a known_destinations file containing the
// single fixture entry these tests use, packed by Python's
// RNS.vendor.umsgpack — the exact packer RNS.Identity.save_known_destinations
// uses (RNS/Identity.py:201). When includeUseTS is true the entry is the
// v1.3.0+ 5-element form [timestamp, packet_hash, public_key, app_data, 0];
// otherwise it is the pre-v1.3.0 4-element form. The file is written at
// <storagePath>/known_destinations and the packed bytes are returned so callers
// can assert byte-parity against a captured golden. Gated on SkipIfNoPythonRNS.
func pythonWriteKnownDestinations(t *testing.T, storagePath string, includeUseTS bool) []byte {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	if err := os.MkdirAll(storagePath, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dest := filepath.Join(storagePath, "known_destinations")
	script := `
import sys, os
import RNS.vendor.umsgpack as umsgpack
path = sys.argv[1]
include_use = sys.argv[2] == '1'
desthash = bytes([0xaa,0xbb,0xcc,0xdd,0xee,0xff,0x00,0x11,0x22,0x33,0x44,0x55,0x66,0x77,0x88,0x99])
ts = 1700000000.0
pkt = b'\x77'*32
pub = bytes(range(0x00,0x40))
app = b'\xde\xad\xbe\xef'
entry = [ts, pkt, pub, app, 0] if include_use else [ts, pkt, pub, app]
kd = {desthash: entry}
os.makedirs(os.path.dirname(path) or '.', exist_ok=True)
with open(path,'wb') as f: umsgpack.dump(kd, f)
sys.stdout.write(open(path,'rb').read().hex())
`
	out := testutils.RunPython(t, script, dest, boolStr(includeUseTS))
	b, err := hex.DecodeString(out)
	if err != nil {
		t.Fatalf("decode python known_destinations hex %q: %v", out, err)
	}
	return b
}

// boolStr returns "1" or "0" for a bool, for passing to a Python script arg.
func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// pyKnownDestEntry is the JSON-decoded view of a single known_destinations
// entry as Python's umsgpack.load sees it (hex-encoded byte fields).
type pyKnownDestEntry struct {
	DestHash   string   `json:"dest_hash"`
	Timestamp  float64  `json:"timestamp"`
	PacketHash string   `json:"packet_hash"`
	PublicKey  string   `json:"public_key"`
	AppData    string   `json:"app_data"`
	UseTS      *float64 `json:"use_ts"`
}

// pythonLoadKnownDestinationsEntry loads a known_destinations file with
// Python's RNS.vendor.umsgpack.load (the exact loader
// RNS.Identity.load_known_destinations uses, RNS/Identity.py:218) and returns
// the single entry's fields. It fails the test if the file does not parse or
// does not contain exactly one entry — so a Go-written file with str map keys
// (instead of bin) would raise umsgpack.InvalidStringException in Python and
// fail here, proving write-side parity. Gated on SkipIfNoPythonRNS.
func pythonLoadKnownDestinationsEntry(t *testing.T, path string) pyKnownDestEntry {
	t.Helper()
	testutils.SkipIfNoPythonRNS(t)
	script := `
import sys, json
import RNS.vendor.umsgpack as umsgpack
with open(sys.argv[1],'rb') as f: kd = umsgpack.load(f)
if len(kd) != 1:
    raise AssertionError("expected exactly 1 known_destinations entry, got "+str(len(kd)))
((k, v),) = kd.items()
result = {
  'dest_hash': k.hex(),
  'timestamp': v[0],
  'packet_hash': v[1].hex(),
  'public_key': v[2].hex(),
  'app_data': v[3].hex(),
  'use_ts': (v[4] if len(v) > 4 else None),
}
print(json.dumps(result))
`
	out := testutils.RunPython(t, script, path)
	var e pyKnownDestEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &e); err != nil {
		t.Fatalf("parse python known_destinations entry: %v\nraw: %s", err, out)
	}
	return e
}

// TestLoadKnownDestinationsMigrates4To5Elements verifies that a
// Python-written 4-element known_destinations file (pre-v1.3.0 layout) is
// migrated on load to the 5-element layout by appending a use-timestamp of 0
// (never used), matching Python Identity.load_known_destinations
// (RNS/Identity.py:226-228).
func TestLoadKnownDestinationsMigrates4To5Elements(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-migrate-")
	pythonWriteKnownDestinations(t, storagePath, false)

	ts.LoadKnownDestinations(storagePath)

	ts.mu.Lock()
	entry, ok := ts.knownDestinations[string(goldenKnownDestDestHash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("golden 4-element entry was not loaded")
	}
	if got, want := len(entry), 5; got != want {
		t.Fatalf("entry len = %d, want %d (migrated to 5 elements)", got, want)
	}
	// Elements 0-3 must be preserved verbatim.
	if _, ok := entry[0].(float64); !ok {
		t.Fatalf("entry[0] type = %T, want float64 (timestamp)", entry[0])
	}
	if got := entry[0].(float64); got != 1700000000.0 {
		t.Fatalf("entry[0] = %v, want 1700000000.0", got)
	}
	if got, ok := entry[1].([]byte); !ok || len(got) != 32 {
		t.Fatalf("entry[1] = %#v, want 32-byte packet hash", entry[1])
	}
	if got, ok := entry[2].([]byte); !ok || len(got) != 64 {
		t.Fatalf("entry[2] = %#v, want 64-byte public key", entry[2])
	}
	if got, ok := entry[3].([]byte); !ok || string(got) != "\xde\xad\xbe\xef" {
		t.Fatalf("entry[3] = %#v, want deadbeef app data", entry[3])
	}
	// The migrated 5th element must be 0 (never used).
	got, ok := numericValue(entry[4])
	if !ok || got != 0 {
		t.Fatalf("entry[4] = %#v, want numeric 0 (never used)", entry[4])
	}
}

// TestLoadKnownDestinationsKeeps5ElementEntry verifies that a
// 5-element entry (already v1.3.0 layout) loads unchanged with its 5th
// element preserved.
func TestLoadKnownDestinationsKeeps5ElementEntry(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-5elem-")
	pythonWriteKnownDestinations(t, storagePath, true)

	ts.LoadKnownDestinations(storagePath)

	ts.mu.Lock()
	entry, ok := ts.knownDestinations[string(goldenKnownDestDestHash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("golden 5-element entry was not loaded")
	}
	if got, want := len(entry), 5; got != want {
		t.Fatalf("entry len = %d, want %d", got, want)
	}
	got, ok := numericValue(entry[4])
	if !ok || got != 0 {
		t.Fatalf("entry[4] = %#v, want numeric 0", entry[4])
	}
}

// TestRememberAppendsFifthElement verifies that Remember creates a
// 5-element entry with the 5th use-timestamp set to 0 (never used), and a
// re-Remember of an existing destination preserves the 5th element rather
// than truncating back to 4 (Python Identity.remember, RNS/Identity.py:101-113).
func TestRememberAppendsFifthElement(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	pubKey := mustTestNewIdentity(t, true).GetPublicKey()
	destHash := []byte("remember-5th-dest-hash!!!")

	ts.Remember([]byte("pkt-1"), destHash, pubKey, []byte("app-1"))

	ts.mu.Lock()
	entry, ok := ts.knownDestinations[string(destHash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("Remember did not create the entry")
	}
	if got, want := len(entry), 5; got != want {
		t.Fatalf("new entry len = %d, want %d", got, want)
	}
	if got, ok := numericValue(entry[4]); !ok || got != 0 {
		t.Fatalf("new entry[4] = %#v, want numeric 0 (never used)", entry[4])
	}

	// Simulate a prior use: set the 5th element to a use timestamp, then
	// re-Remember. The use timestamp must be preserved (Python remember does
	// not touch element 4 on an existing entry).
	ts.mu.Lock()
	entry[4] = float64(1700000000.0)
	ts.mu.Unlock()

	ts.Remember([]byte("pkt-2"), destHash, pubKey, []byte("app-2"))

	ts.mu.Lock()
	entry, ok = ts.knownDestinations[string(destHash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("re-Remember dropped the entry")
	}
	if got, want := len(entry), 5; got != want {
		t.Fatalf("re-remembered entry len = %d, want %d", got, want)
	}
	if got, ok := numericValue(entry[4]); !ok || got != 1700000000.0 {
		t.Fatalf("re-remembered entry[4] = %#v, want 1700000000.0 (preserved)", entry[4])
	}
	// Elements 1 and 3 should reflect the new Remember call.
	if got, ok := entry[3].([]byte); !ok || string(got) != "app-2" {
		t.Fatalf("re-remembered entry[3] = %#v, want app-2", entry[3])
	}
}

// TestRecallAppDataReturnsAppData verifies that RecallAppData
// returns the app_data (element 3) for a known destination (Python
// Identity.recall_app_data, RNS/Identity.py:162-175).
func TestRecallAppDataReturnsAppData(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-appdata-")
	pythonWriteKnownDestinations(t, storagePath, true)

	ts.LoadKnownDestinations(storagePath)

	got := ts.RecallAppData(goldenKnownDestDestHash)
	if got == nil {
		t.Fatal("RecallAppData returned nil for a known destination")
	}
	if string(got) != "\xde\xad\xbe\xef" {
		t.Fatalf("RecallAppData = %x, want deadbeef", got)
	}
}

// TestGoldenKnownDestinationsHexMatchesPython validates that the hardcoded
// goldenKnownDestinations4Hex / goldenKnownDestinations5Hex constants equal
// the bytes Python's RNS.vendor.umsgpack actually produces for the same
// fixture entry — so the shared golden literals (also used by the
// recombine/save test files) are proven Python-faithful rather than
// hand-typed. Gated on SkipIfNoPythonRNS.
func TestGoldenKnownDestinationsHexMatchesPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	dir := testutils.TempDir(t, "rns-kd-golden-")
	got4 := pythonWriteKnownDestinations(t, dir, false)
	if hex.EncodeToString(got4) != goldenKnownDestinations4Hex {
		t.Fatalf("4-element known_destinations: live Python bytes != golden hex\n got: %x\nwant: %s", got4, goldenKnownDestinations4Hex)
	}

	dir2 := testutils.TempDir(t, "rns-kd-golden5-")
	got5 := pythonWriteKnownDestinations(t, dir2, true)
	if hex.EncodeToString(got5) != goldenKnownDestinations5Hex {
		t.Fatalf("5-element known_destinations: live Python bytes != golden hex\n got: %x\nwant: %s", got5, goldenKnownDestinations5Hex)
	}
}

// TestKnownDestinationsRoundTripPythonGoPython is the cross-implementation
// round-trip parity proof for the known_destinations on-disk format: Python
// writes the file (umsgpack.dump) -> Go loads it (LoadKnownDestinations) ->
// Go saves it (SaveKnownDestinations) -> Python loads the Go-written file
// (umsgpack.load) and the entry fields round-trip exactly. This exercises both
// read and write parity, including the binary (0xc4) msgpack map-key layout
// (a Go-written str key would raise umsgpack.InvalidStringException in the
// final Python load). Gated on SkipIfNoPythonRNS.
func TestKnownDestinationsRoundTripPythonGoPython(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	// 1. Python writes a 5-element known_destinations file.
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	pyDir := testutils.TempDir(t, "rns-kd-rt-py-")
	pythonWriteKnownDestinations(t, pyDir, true)

	// 2. Go loads it and verifies the entry fields.
	ts.LoadKnownDestinations(pyDir)
	ts.mu.Lock()
	entry, ok := ts.knownDestinations[string(goldenKnownDestDestHash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("Go did not load the Python-written known_destinations entry")
	}
	if got, want := len(entry), 5; got != want {
		t.Fatalf("Go-loaded entry len = %d, want %d", got, want)
	}
	if ts, ok := entry[0].(float64); !ok || ts != 1700000000.0 {
		t.Fatalf("Go-loaded entry[0] = %#v, want float64 1700000000.0", entry[0])
	}
	if got, ok := entry[1].([]byte); !ok || len(got) != 32 {
		t.Fatalf("Go-loaded entry[1] = %#v, want 32-byte packet hash", entry[1])
	}
	if got, ok := entry[2].([]byte); !ok || len(got) != 64 {
		t.Fatalf("Go-loaded entry[2] = %#v, want 64-byte public key", entry[2])
	}
	if got, ok := entry[3].([]byte); !ok || string(got) != "\xde\xad\xbe\xef" {
		t.Fatalf("Go-loaded entry[3] = %#v, want deadbeef app data", entry[3])
	}
	if got, ok := numericValue(entry[4]); !ok || got != 0 {
		t.Fatalf("Go-loaded entry[4] = %#v, want numeric 0 (never used)", entry[4])
	}

	// 3. Go saves the loaded table to a fresh storage path.
	goDir := testutils.TempDir(t, "rns-kd-rt-go-")
	ts.SaveKnownDestinations(goDir)
	goFile := filepath.Join(goDir, "known_destinations")
	if _, err := os.Stat(goFile); err != nil {
		t.Fatalf("Go did not write known_destinations: %v", err)
	}

	// 4. Python loads the Go-written file and the entry round-trips exactly.
	got := pythonLoadKnownDestinationsEntry(t, goFile)
	if got.DestHash != knownDestFixtureDestHashHex {
		t.Fatalf("round-trip dest_hash = %s, want %s", got.DestHash, knownDestFixtureDestHashHex)
	}
	if got.Timestamp != 1700000000.0 {
		t.Fatalf("round-trip timestamp = %v, want 1700000000.0", got.Timestamp)
	}
	if got.PacketHash != strings.Repeat("77", 32) {
		t.Fatalf("round-trip packet_hash = %s, want %x... (32x 0x77)", got.PacketHash, 0x77)
	}
	if got.PublicKey != hex.EncodeToString(bytesRange(0x00, 0x40)) {
		t.Fatalf("round-trip public_key = %s, want 00..3f (64 bytes)", got.PublicKey)
	}
	if got.AppData != "deadbeef" {
		t.Fatalf("round-trip app_data = %s, want deadbeef", got.AppData)
	}
	if got.UseTS == nil || *got.UseTS != 0 {
		t.Fatalf("round-trip use_ts = %v, want 0", got.UseTS)
	}
}

// bytesRange returns the byte slice [start, end) (e.g. bytesRange(0x00,0x40)
// = 0x00..0x3f), mirroring Python's bytes(range(0x00,0x40)) used for the
// fixture public key.
func bytesRange(start, end int) []byte {
	b := make([]byte, 0, end-start)
	for i := start; i < end; i++ {
		b = append(b, byte(i))
	}
	return b
}
