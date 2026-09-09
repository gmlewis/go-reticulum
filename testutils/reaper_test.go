// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// runSleeper starts a detached `sleep N` child and returns its PID.
func runSleeper(t *testing.T, secs int) (int, *exec.Cmd) {
	t.Helper()
	cmd := exec.Command("sleep", strconv.Itoa(secs))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd.Process.Pid, cmd
}

// killAndReap kills a process and reaps it so it stops satisfying
// `kill -0` (an unreaped zombie still answers kill -0, and the real go test
// parent reaps the dead test binary the same way).
func killAndReap(t *testing.T, pid int, cmd *exec.Cmd) {
	t.Helper()
	if err := exec.Command("kill", "-9", strconv.Itoa(pid)).Run(); err != nil {
		t.Fatalf("kill %d: %v", pid, err)
	}
	_ = cmd.Wait()
}

// procAlive reports whether the PID still exists. A reaped-or-not zombie
// counts as dead: the watchdog only ever acts on the (already killed, not
// yet reaped by us) child, and kill -0 succeeds on zombies.
func procAlive(pid int) bool {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	stat := strings.TrimSpace(string(out))
	return stat != "" && !strings.HasPrefix(stat, "Z")
}

// TestReaperKillsChildWhenParentDies proves the watchdog fires: a decoy
// stands in for the test binary's PID; the watchdog must SIGKILL the child
// once the decoy is gone, and NOT signal an unrelated process that merely
// recycled the child's PID (the fingerprint check).
func TestReaperKillsChildWhenParentDies(t *testing.T) {
	t.Parallel()

	decoy, decoyCmd := runSleeper(t, 30)
	child, _ := runSleeper(t, 300)
	childFingerprint := psLstart(child)
	if childFingerprint == "" {
		t.Skip("ps -o lstart= unavailable")
	}
	armReaper(decoy, child, childFingerprint)

	// Kill (and reap) the decoy "test binary"; the watchdog should kill
	// the child within a couple of poll intervals.
	killAndReap(t, decoy, decoyCmd)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !procAlive(child) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("watchdog never killed the child (pid %d still alive)", child)
}

// TestReaperRefusesFingerprintMismatch pins the recycled-PID guard: when the
// child PID's start time no longer matches the fingerprint, the watchdog
// must not signal anything. The decoy here is replaced by an unrelated
// long-running process carrying a WRONG fingerprint for the child.
func TestReaperRefusesFingerprintMismatch(t *testing.T) {
	t.Parallel()

	decoy, decoyCmd := runSleeper(t, 30)
	child, _ := runSleeper(t, 300)
	armReaper(decoy, child, "impossible-fingerprint")

	killAndReap(t, decoy, decoyCmd)
	// Give the watchdog ample time to (wrongly) act; it must not.
	time.Sleep(3 * time.Second)
	if !procAlive(child) {
		t.Fatal("watchdog killed the child despite the fingerprint mismatch")
	}
}
