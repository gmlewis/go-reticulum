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

// ratchetFilePath returns the on-disk path of the ratchet file for destHash
// under storagePath (ratchets/<lowercase-hex>, matching persistRatchet and
// Python RNS.hexrep(delimit=False)).
func ratchetFilePath(storagePath string, destHash []byte) string {
	return filepath.Join(storagePath, "ratchets", hexHash(destHash))
}

// packRatchetFile serializes a ratchet record {"ratchet": bytes, "received":
// float seconds} in the same on-disk format persistRatchet writes, so a test
// can hand-write a ratchet file with a controlled received timestamp.
func packRatchetFile(ratchetPub []byte, receivedSec float64) ([]byte, error) {
	return msgpack.Pack(map[string]any{
		"ratchet":  ratchetPub,
		"received": receivedSec,
	})
}

// TestCleanRatchetsRemovesRatchetForUnknownDestination verifies that
// CleanRatchets removes a ratchet file whose destination hash is NOT a key in
// knownDestinations (Python Identity._clean_ratchets "unknown" branch,
// RNS/Identity.py:470-471,474-475).
func TestCleanRatchetsRemovesRatchetForUnknownDestination(t *testing.T) {
	t.Parallel()
	storagePath := testutils.TempDir(t, "rns-clean-ratchet-unknown-")
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	// A valid, fresh (non-expired) ratchet file for a destination that is NOT
	// in knownDestinations — the sole reason to remove it is that it is unknown.
	unknownHash := []byte("unknown-ratchet-dh!")
	ts.persistRatchet(storagePath, unknownHash, []byte("ratchet-pubkey-bytes-32-bytes-long!!!"))
	rp := ratchetFilePath(storagePath, unknownHash)
	if _, err := os.Stat(rp); err != nil {
		t.Fatalf("setup: ratchet file not written: %v", err)
	}

	ts.CleanRatchets()

	if _, err := os.Stat(rp); !os.IsNotExist(err) {
		t.Fatalf("ratchet file for unknown destination still exists after CleanRatchets: %v", err)
	}
}

// TestCleanRatchetsKeepsRatchetForKnownDestination verifies that a
// fresh, non-expired ratchet file for a destination that IS in
// knownDestinations is kept by CleanRatchets (neither expired, corrupted, nor
// unknown).
func TestCleanRatchetsKeepsRatchetForKnownDestination(t *testing.T) {
	t.Parallel()
	storagePath := testutils.TempDir(t, "rns-clean-ratchet-known-")
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	knownHash := []byte("known-ratchet-dh!!!")
	// Register the destination so its hash is a key in knownDestinations.
	ts.Remember([]byte("pkt-known"), knownHash, mustTestNewIdentity(t, true).GetPublicKey(), []byte("app"))
	ts.persistRatchet(storagePath, knownHash, []byte("ratchet-pubkey-bytes-32-bytes-long!!!"))
	rp := ratchetFilePath(storagePath, knownHash)
	if _, err := os.Stat(rp); err != nil {
		t.Fatalf("setup: ratchet file not written: %v", err)
	}

	ts.CleanRatchets()

	if _, err := os.Stat(rp); err != nil {
		t.Fatalf("ratchet file for known destination was removed by CleanRatchets: %v", err)
	}
}

// TestCleanRatchetsRemovesExpiredRatchet verifies that an expired
// ratchet file (received + RATCHET_EXPIRY in the past) is removed even for a
// known destination (Python Identity._clean_ratchets "expired" branch,
// RNS/Identity.py:462-463).
func TestCleanRatchetsRemovesExpiredRatchet(t *testing.T) {
	t.Parallel()
	storagePath := testutils.TempDir(t, "rns-clean-ratchet-expired-")
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	ts.mu.Lock()
	ts.storagePath = storagePath
	ts.mu.Unlock()

	knownHash := []byte("expired-ratchet-dh!")
	ts.Remember([]byte("pkt-exp"), knownHash, mustTestNewIdentity(t, true).GetPublicKey(), []byte("app"))

	// Hand-write an expired ratchet file: received = now - 31 days (past the
	// 30-day RATCHET_EXPIRY).
	ratchetDir := filepath.Join(storagePath, "ratchets")
	if err := os.MkdirAll(ratchetDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	expiredSec := float64(time.Now().UnixNano())/1e9 - 31*24*3600
	data, err := packRatchetFile([]byte("ratchet-pubkey-bytes-32-bytes-long!!!"), expiredSec)
	if err != nil {
		t.Fatalf("pack ratchet: %v", err)
	}
	rp := ratchetFilePath(storagePath, knownHash)
	if err := os.WriteFile(rp, data, 0o600); err != nil {
		t.Fatalf("write ratchet: %v", err)
	}

	ts.CleanRatchets()

	if _, err := os.Stat(rp); !os.IsNotExist(err) {
		t.Fatalf("expired ratchet file still exists after CleanRatchets: %v", err)
	}
}
