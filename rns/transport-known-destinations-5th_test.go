// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

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

// TestLoadKnownDestinationsMigrates4To5Elements covers Phase 13 task 1: a
// Python-written 4-element known_destinations file (pre-v1.3.0 layout) is
// migrated on load to the 5-element layout by appending a use-timestamp of 0
// (never used), matching Python Identity.load_known_destinations
// (RNS/Identity.py:226-228).
func TestLoadKnownDestinationsMigrates4To5Elements(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-migrate-")
	writeGoldenKnownDestinations(t, storagePath, goldenKnownDestinations4Hex)

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

// TestLoadKnownDestinationsKeeps5ElementEntry covers Phase 13 task 1: a
// 5-element entry (already v1.3.0 layout) loads unchanged with its 5th
// element preserved.
func TestLoadKnownDestinationsKeeps5ElementEntry(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-5elem-")
	writeGoldenKnownDestinations(t, storagePath, goldenKnownDestinations5Hex)

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

// TestRememberAppendsFifthElement covers Phase 13 task 1: Remember creates a
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

// TestRecallAppDataReturnsAppData covers Phase 13 task 1: RecallAppData
// returns the app_data (element 3) for a known destination (Python
// Identity.recall_app_data, RNS/Identity.py:162-175).
func TestRecallAppDataReturnsAppData(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	storagePath := testutils.TempDir(t, "rns-kd-appdata-")
	writeGoldenKnownDestinations(t, storagePath, goldenKnownDestinations5Hex)

	ts.LoadKnownDestinations(storagePath)

	got := ts.RecallAppData(goldenKnownDestDestHash)
	if got == nil {
		t.Fatal("RecallAppData returned nil for a known destination")
	}
	if string(got) != "\xde\xad\xbe\xef" {
		t.Fatalf("RecallAppData = %x, want deadbeef", got)
	}
}
