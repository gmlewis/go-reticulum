// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// writeFiles creates regular files with the given names inside dir.
func writeFiles(t *testing.T, dir string, names ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create %v: %v", dir, err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("create %v: %v", name, err)
		}
	}
}

// TestCleanAnnounceCacheFiles verifies the disk sweep of Python
// Transport.clean_announce_cache (Transport.py:2532-2552): every announce
// cache file whose packet hash is not referenced by a live path_table or
// tunnel entry is removed, active entries are kept, and unparseable names
// are removed too.
func TestCleanAnnounceCacheFiles(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, tempDirPrefix)
	ts := NewTransportSystem(nil)
	ts.storagePath = dir
	cacheDir := announceCacheDirFor(dir)

	activeHash := FullHash([]byte("active-announce"))
	tunnelHash := FullHash([]byte("tunnel-announce"))

	ts.mu.Lock()
	ts.pathTable = map[string]*PathEntry{
		string(bytes.Repeat([]byte{0xd1}, 16)): {PacketHash: activeHash},
	}
	ts.tunnels = map[string]*Tunnel{
		string(bytes.Repeat([]byte{0xd2}, 8)): {
			ID: bytes.Repeat([]byte{0xd2}, 8),
			Paths: map[string]*PathEntry{
				string(bytes.Repeat([]byte{0xd3}, 16)): {PacketHash: tunnelHash},
			},
		},
	}
	ts.mu.Unlock()

	writeFiles(t, cacheDir,
		"stale-file",   // not referenced: removed
		"not-even-hex", // unparseable name: removed, as in Python
	)
	activeName := hex.EncodeToString(activeHash)
	tunnelName := hex.EncodeToString(tunnelHash)
	writeFiles(t, cacheDir, activeName, tunnelName)
	if err := os.Mkdir(filepath.Join(cacheDir, "subdir-not-file"), 0o700); err != nil {
		t.Fatalf("create subdir: %v", err)
	}

	ts.cleanAnnounceCacheFiles(func(time.Duration) {})

	for _, name := range []string{"stale-file", "not-even-hex"} {
		if _, err := os.Stat(filepath.Join(cacheDir, name)); !os.IsNotExist(err) {
			t.Errorf("file %v was not removed: err=%v", name, err)
		}
	}
	for _, name := range []string{activeName, tunnelName} {
		if _, err := os.Stat(filepath.Join(cacheDir, name)); err != nil {
			t.Errorf("file %v unexpectedly removed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "subdir-not-file")); err != nil {
		t.Errorf("subdir unexpectedly removed: %v", err)
	}
}

// TestCleanCaches verifies the Go port of Python Reticulum.__clean_caches
// (RNS/Reticulum.py:1147-1169): resource cache files older than
// ResourceCache and packet cache files older than DestinationTimeout are
// removed; recent files, non-hex names, and the announces subdirectory are
// left alone.
func TestCleanCaches(t *testing.T) {
	t.Parallel()
	dir := testutils.TempDir(t, tempDirPrefix)
	ts := NewTransportSystem(nil)
	ts.storagePath = dir

	resourcesDir := filepath.Join(dir, "resources")
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(resourcesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cacheDir, "announces"), 0o700); err != nil {
		t.Fatalf("create announces dir: %v", err)
	}

	// 32 hex chars, Python's __clean_caches name rule (len ==
	// (RNS.Identity.HASHLENGTH//8)*2).
	oldName := strings.Repeat("ab", 16)
	newName := oldName[:16] + "ffff"
	aged := time.Now().Add(-48 * time.Hour)

	oldResource := filepath.Join(resourcesDir, oldName)
	if err := os.WriteFile(oldResource, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldResource, aged, aged); err != nil {
		t.Fatal(err)
	}
	newResource := filepath.Join(resourcesDir, newName)
	if err := os.WriteFile(newResource, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	oldPacket := filepath.Join(cacheDir, oldName)
	if err := os.WriteFile(oldPacket, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The packet cache rule is DestinationTimeout (7 days); age the file
	// past it, unlike the resource file's 24-hour rule above.
	packetAged := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(oldPacket, packetAged, packetAged); err != nil {
		t.Fatal(err)
	}
	newPacket := filepath.Join(cacheDir, newName)
	if err := os.WriteFile(newPacket, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	oddName := filepath.Join(cacheDir, "nonhexname")
	if err := os.WriteFile(oddName, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	announce := filepath.Join(cacheDir, "announces", "someannounce")
	if err := os.WriteFile(announce, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	ts.cleanCaches()

	if _, err := os.Stat(oldResource); !os.IsNotExist(err) {
		t.Errorf("aged resource cache file survived: err=%v", err)
	}
	if _, err := os.Stat(newResource); err != nil {
		t.Errorf("recent resource cache file was removed: %v", err)
	}
	if _, err := os.Stat(oldPacket); !os.IsNotExist(err) {
		t.Errorf("aged packet cache file survived: err=%v", err)
	}
	if _, err := os.Stat(newPacket); err != nil {
		t.Errorf("recent packet cache file was removed: %v", err)
	}
	if _, err := os.Stat(oddName); err != nil {
		t.Errorf("non-hex-named file was removed: %v", err)
	}
	if _, err := os.Stat(announce); err != nil {
		t.Errorf("announce inside the announces subdir was removed: %v", err)
	}
}
