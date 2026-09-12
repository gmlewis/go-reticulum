// Copyright 2026 Glenn Lewis. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
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

// writeRatchetFile packs payload into the ratchet file that GetRatchet reads
// for destHash and returns a transport rooted at a fresh storage directory.
func writeRatchetFile(t *testing.T, destHash []byte, payload any) *TransportSystem {
	t.Helper()

	tmpDir := tempDir(t)
	storagePath := filepath.Join(tmpDir, "storage")
	ratchetDir := filepath.Join(storagePath, "ratchets")
	if err := os.MkdirAll(ratchetDir, 0o700); err != nil {
		t.Fatalf("failed to create ratchet dir: %v", err)
	}

	data, err := msgpack.Pack(payload)
	if err != nil {
		t.Fatalf("Pack ratchet payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ratchetDir, fmt.Sprintf("%x", destHash)), data, 0o600); err != nil {
		t.Fatalf("WriteFile ratchet: %v", err)
	}

	ts := NewTransportSystem(nil)
	ts.storagePath = storagePath
	return ts
}

// TestGetRatchetLoadsValidFile is the happy-path control for the malformed
// cases below: a well-formed, unexpired ratchet file is still served.
func TestGetRatchetLoadsValidFile(t *testing.T) {
	t.Parallel()

	destHash := []byte("valid_ratchet_dest_hash")
	ratchetPub := bytes.Repeat([]byte{0x5a}, 32)
	ts := writeRatchetFile(t, destHash, map[string]any{
		"ratchet":  ratchetPub,
		"received": float64(time.Now().UnixNano()) / 1e9,
	})

	if got := ts.GetRatchet(destHash); !bytes.Equal(got, ratchetPub) {
		t.Fatalf("GetRatchet=%x, want %x", got, ratchetPub)
	}
}

// TestGetRatchetMalformedFile pins how GetRatchet treats ratchet storage it
// cannot use. Python wraps the read in try/except, logs the error, and returns
// None (Identity.py:488-500), so a malformed or foreign-written ratchet file
// must be ignored instead of panicking the process on an unguarded type
// assertion.
func TestGetRatchetMalformedFile(t *testing.T) {
	t.Parallel()

	validPub := bytes.Repeat([]byte{0x42}, 32)

	tests := []struct {
		name    string
		payload any
	}{
		{name: "not a map", payload: []any{1, 2, 3}},
		{name: "ratchet is a string", payload: map[string]any{"ratchet": "not-bytes", "received": 1.0}},
		{name: "ratchet is an int", payload: map[string]any{"ratchet": 7, "received": 1.0}},
		{name: "received is a string", payload: map[string]any{"ratchet": validPub, "received": "recently"}},
		{name: "received is a map", payload: map[string]any{"ratchet": validPub, "received": map[string]any{}}},
		{name: "missing ratchet", payload: map[string]any{"received": 1.0}},
		{name: "missing received", payload: map[string]any{"ratchet": validPub}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			destHash := []byte("malformed_ratchet_dest_hash")
			ts := writeRatchetFile(t, destHash, tt.payload)

			if got := ts.GetRatchet(destHash); got != nil {
				t.Fatalf("GetRatchet on %v returned %x, want nil", tt.name, got)
			}
		})
	}
}
