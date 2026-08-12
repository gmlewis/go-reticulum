// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestSavePacketHashlistRawFormat covers Phase 12 task 1: SavePacketHashlist
// writes packet_hashlist.raw as exactly n*HashLength/8 raw concatenated
// bytes (RNS/Transport.py:3315-3324), and the set round-trips through
// LoadPacketHashlist.
func TestSavePacketHashlistRawFormat(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)
	tmpDir := testutils.TempDir(t, "rns-hashlist-raw-")

	hashLen := HashLength / 8
	hashes := make([][]byte, 5)
	for i := range hashes {
		h := make([]byte, hashLen)
		h[0] = byte(i + 1)
		h[1] = 0xAB
		hashes[i] = h
	}

	ts.mu.Lock()
	for _, h := range hashes {
		ts.packetHashes[string(h)] = time.Now()
	}
	ts.mu.Unlock()

	if err := ts.SavePacketHashlist(tmpDir); err != nil {
		t.Fatalf("SavePacketHashlist: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "packet_hashlist.raw"))
	if err != nil {
		t.Fatalf("read packet_hashlist.raw: %v", err)
	}
	if got, want := len(data), len(hashes)*hashLen; got != want {
		t.Fatalf("packet_hashlist.raw size = %v, want %v (n*HashLength/8)", got, want)
	}

	// The raw bytes are exactly the concatenated hashes. Build the expected
	// buffer in insertion-independent order by sorting, since map iteration
	// is unordered.
	gotSet := make(map[string]bool, len(hashes))
	for i := 0; i+hashLen <= len(data); i += hashLen {
		gotSet[string(data[i:i+hashLen])] = true
	}
	for _, h := range hashes {
		if !gotSet[string(h)] {
			t.Fatalf("hash %x missing from packet_hashlist.raw", h)
		}
	}
	if len(gotSet) != len(hashes) {
		t.Fatalf("packet_hashlist.raw has %v distinct hashes, want %v", len(gotSet), len(hashes))
	}

	// Round-trip: load into a fresh transport and confirm every hash returns.
	ts2 := NewTransportSystem(nil)
	ts2.SetEnabled(true)
	if err := ts2.LoadPacketHashlist(tmpDir); err != nil {
		t.Fatalf("LoadPacketHashlist: %v", err)
	}
	ts2.mu.Lock()
	for _, h := range hashes {
		if _, ok := ts2.packetHashes[string(h)]; !ok {
			t.Fatalf("hash %x not present after LoadPacketHashlist", h)
		}
	}
	ts2.mu.Unlock()

	// No legacy msgpack file should have been written.
	if _, err := os.Stat(filepath.Join(tmpDir, "packet_hashlist")); err == nil {
		t.Fatal("legacy packet_hashlist file should not exist after raw save")
	}
}

// TestLoadPacketHashlistLegacyMigration covers Phase 12 task 1: when
// packet_hashlist.raw is absent, LoadPacketHashlist falls back to the legacy
// msgpack packet_hashlist file (an array of hash byte strings) and migrates
// the entries into packetHashes.
func TestLoadPacketHashlistLegacyMigration(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-hashlist-legacy-")
	hashLen := HashLength / 8

	hashes := make([][]byte, 3)
	legacy := make([]any, 3)
	for i := range hashes {
		h := make([]byte, hashLen)
		h[0] = byte(0x10 + i)
		legacy[i] = h
		hashes[i] = h
	}
	packed, err := msgpack.Pack(legacy)
	if err != nil {
		t.Fatalf("msgpack.Pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "packet_hashlist"), packed, 0o600); err != nil {
		t.Fatalf("write legacy packet_hashlist: %v", err)
	}

	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)
	if err := ts.LoadPacketHashlist(tmpDir); err != nil {
		t.Fatalf("LoadPacketHashlist: %v", err)
	}
	ts.mu.Lock()
	for _, h := range hashes {
		if _, ok := ts.packetHashes[string(h)]; !ok {
			t.Fatalf("legacy hash %x not migrated into packetHashes", h)
		}
	}
	ts.mu.Unlock()
}

// TestLoadPacketHashlistRawTakesPrecedenceOverLegacy covers Phase 12 task 1:
// when both packet_hashlist.raw and the legacy packet_hashlist file exist,
// the .raw file is loaded and the legacy file is ignored.
func TestLoadPacketHashlistRawTakesPrecedenceOverLegacy(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-hashlist-precedence-")
	hashLen := HashLength / 8

	rawHash := make([]byte, hashLen)
	rawHash[0] = 0x01
	if err := os.WriteFile(filepath.Join(tmpDir, "packet_hashlist.raw"), rawHash, 0o600); err != nil {
		t.Fatalf("write raw: %v", err)
	}

	legacyOnlyHash := make([]byte, hashLen)
	legacyOnlyHash[0] = 0x02
	packed, err := msgpack.Pack([]any{legacyOnlyHash})
	if err != nil {
		t.Fatalf("msgpack.Pack: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "packet_hashlist"), packed, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)
	if err := ts.LoadPacketHashlist(tmpDir); err != nil {
		t.Fatalf("LoadPacketHashlist: %v", err)
	}
	ts.mu.Lock()
	_, hasRaw := ts.packetHashes[string(rawHash)]
	_, hasLegacy := ts.packetHashes[string(legacyOnlyHash)]
	ts.mu.Unlock()
	if !hasRaw {
		t.Fatal("raw hash missing: .raw should take precedence")
	}
	if hasLegacy {
		t.Fatal("legacy-only hash loaded: .raw should take precedence over legacy")
	}
}

// TestSavePacketHashlistDisabledNoWrite covers Phase 12 task 1: a disabled
// transport writes no file (RNS/Transport.py:3294,3310).
func TestSavePacketHashlistDisabledNoWrite(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil) // not enabled
	tmpDir := testutils.TempDir(t, "rns-hashlist-disabled-")

	hashLen := HashLength / 8
	h := make([]byte, hashLen)
	ts.mu.Lock()
	ts.packetHashes[string(h)] = time.Now()
	ts.mu.Unlock()

	if err := ts.SavePacketHashlist(tmpDir); err != nil {
		t.Fatalf("SavePacketHashlist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "packet_hashlist.raw")); err == nil {
		t.Fatal("packet_hashlist.raw should not be written when transport is disabled")
	}
}

// TestLoadPacketHashlistDisabledNoLoad covers Phase 12 task 1: a disabled
// transport loads nothing (RNS/Transport.py:243).
func TestLoadPacketHashlistDisabledNoLoad(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-hashlist-load-disabled-")
	hashLen := HashLength / 8
	rawHash := make([]byte, hashLen)
	rawHash[0] = 0x07
	if err := os.WriteFile(filepath.Join(tmpDir, "packet_hashlist.raw"), rawHash, 0o600); err != nil {
		t.Fatalf("write raw: %v", err)
	}

	ts := NewTransportSystem(nil) // not enabled
	if err := ts.LoadPacketHashlist(tmpDir); err != nil {
		t.Fatalf("LoadPacketHashlist: %v", err)
	}
	ts.mu.Lock()
	_, loaded := ts.packetHashes[string(rawHash)]
	ts.mu.Unlock()
	if loaded {
		t.Fatal("packet hash loaded despite transport being disabled")
	}
}
