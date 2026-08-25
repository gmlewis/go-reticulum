// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// tempDir creates a short-path temp dir (avoids the macOS t.TempDir socket-path
// length pitfall) cleaned up with t.Cleanup.
func tempDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "gornsd-lock-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestAcquireInstanceLockSingleton verifies a second acquirer on the same lock
// path is refused and given the holder's PID, while the first holder keeps the
// lock until it releases.
func TestAcquireInstanceLockSingleton(t *testing.T) {
	lockPath := filepath.Join(tempDir(t), "gornsd.lock")

	release1, holderPID, err := acquireInstanceLock(lockPath)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if release1 == nil {
		t.Fatal("first acquire should succeed (release != nil)")
	}
	if holderPID != 0 {
		t.Fatalf("first acquire holderPID = %v, want 0", holderPID)
	}
	t.Cleanup(release1)

	release2, holderPID2, err := acquireInstanceLock(lockPath)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if release2 != nil {
		t.Fatal("second acquire should be refused (release == nil)")
	}
	if holderPID2 != os.Getpid() {
		t.Fatalf("second acquire holderPID = %v, want %v", holderPID2, os.Getpid())
	}
}
