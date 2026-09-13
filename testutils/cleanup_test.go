// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestSignalCleanupHelperProcess is not a test: TestSignalCleanupRemovesTempDirs
// re-runs this binary with -test.run pointed at it, and it stands in for a test
// run that is interrupted. It creates a temp dir through TempDir, records the
// path for its parent, and then blocks until the parent interrupts it.
func TestSignalCleanupHelperProcess(t *testing.T) {
	marker := os.Getenv("TESTUTILS_SIGNAL_CLEANUP_MARKER")
	if marker == "" {
		t.Skip("helper for TestSignalCleanupRemovesTempDirs")
	}

	dir := TempDir(t, "testutils-signal-cleanup-")
	if err := os.WriteFile(marker, []byte(dir), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	// Blocked until the parent signals us: t.Cleanup must NOT get a chance to
	// run, which is exactly the situation this test exists to cover.
	time.Sleep(time.Hour)
}

// TestSignalCleanupRemovesTempDirs proves the SIGINT/SIGTERM handler removes
// the directories a run created when that run is interrupted — the case
// t.Cleanup and defer can never cover, and the reason interrupted runs used to
// strand their temp dirs in /tmp.
func TestSignalCleanupRemovesTempDirs(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "dir-from-child")

	cmd := exec.Command(os.Args[0], "-test.run=TestSignalCleanupHelperProcess")
	cmd.Env = append(os.Environ(), "TESTUTILS_SIGNAL_CLEANUP_MARKER="+marker)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start interrupted-run helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	dir := waitForMarker(t, marker)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("child temp dir %v missing before the signal: %v", dir, err)
	}

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal child: %v", err)
	}
	// The handler re-raises the signal, so the child still dies of SIGINT.
	_ = cmd.Wait()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("child temp dir %v survived SIGINT (stat err = %v)", dir, err)
	}
}

// waitForMarker waits for the helper to record the temp dir it created.
func waitForMarker(t *testing.T, marker string) string {
	t.Helper()

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("helper never recorded a temp dir in %v", marker)
	return ""
}
