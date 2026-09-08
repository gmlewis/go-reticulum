// Copyright 2026 Glenn Lewis. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func tempDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "rns-ratchet-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestSetRatchetSharedInstance verifies that a transport connected to a shared
// Reticulum instance does NOT persist ratchets to disk, mirroring Python
// Identity.py:420 (`if not RNS.Transport.owner.is_connected_to_shared_instance:`).
func TestSetRatchetSharedInstance(t *testing.T) {
	t.Parallel()

	tmpDir := tempDir(t)
	storagePath := filepath.Join(tmpDir, "storage")
	if err := os.MkdirAll(storagePath, 0o700); err != nil {
		t.Fatalf("failed to create storage dir: %v", err)
	}

	ts := NewTransportSystem(nil)
	ts.storagePath = storagePath
	ts.running = true
	ts.SetConnectedToSharedInstance(true)

	destHash := []byte("0123456789abcdef")
	ratchetPub := bytes.Repeat([]byte{0x42}, 32)

	ts.SetRatchet(destHash, ratchetPub)

	// Verify in-memory cache is populated
	ratchet := ts.GetRatchet(destHash)
	if !bytes.Equal(ratchet, ratchetPub) {
		t.Fatalf("in-memory ratchet mismatch: got %x, want %x", ratchet, ratchetPub)
	}

	// Give any errant background write a moment to run
	time.Sleep(50 * time.Millisecond)

	// Verify NO file was written to disk when connected to shared instance
	ratchetFile := filepath.Join(storagePath, "ratchets", "30313233343536373839616263646566")
	if _, err := os.Stat(ratchetFile); !os.IsNotExist(err) {
		t.Fatalf("expected ratchet file to NOT exist when connected to shared instance, but stat returned: %v", err)
	}
}

// TestSetRatchetStandalonePersists verifies that a standalone transport instance
// (not connected to a shared instance) persists ratchets to disk asynchronously.
func TestSetRatchetStandalonePersists(t *testing.T) {
	t.Parallel()

	tmpDir := tempDir(t)
	storagePath := filepath.Join(tmpDir, "storage")
	if err := os.MkdirAll(storagePath, 0o700); err != nil {
		t.Fatalf("failed to create storage dir: %v", err)
	}

	ts := NewTransportSystem(nil)
	ts.storagePath = storagePath
	ts.running = true
	ts.SetConnectedToSharedInstance(false)

	destHash := []byte("fedcba9876543210")
	ratchetPub := bytes.Repeat([]byte{0x77}, 32)

	ts.SetRatchet(destHash, ratchetPub)

	// Wait for asynchronous persist to complete
	ratchetFile := filepath.Join(storagePath, "ratchets", "66656463626139383736353433323130")
	deadline := time.Now().Add(2 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ratchetFile); err == nil {
			found = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !found {
		t.Fatalf("expected ratchet file to be persisted at %v", ratchetFile)
	}
}
