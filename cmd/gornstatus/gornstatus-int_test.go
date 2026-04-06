// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func buildGornstatus(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, cleanup := testutils.TempDir(t, "gornstatus-test-")
	bin := filepath.Join(tmpDir, "gornstatus")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		t.Fatalf("failed to build gornstatus: %v\n%v", err, string(out))
	}
	return bin, cleanup
}

func TestIntegration_VersionOutput(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornstatus --version failed: %v\n%v", err, string(out))
	}
	want := "gornstatus " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestIntegration_HelpOutput(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	out, err := exec.Command(bin, "--help").CombinedOutput()
	_ = err
	output := string(out)
	for _, want := range []string{
		"Reticulum Network Stack Status",
		"--config",
		"--version",
		"-a, --all",
		"-A, --announce-stats",
		"-l, --link-stats",
		"-t, --totals",
		"-s SORT, --sort SORT",
		"-r, --reverse",
		"-j, --json",
		"-R hash",
		"-i path",
		"-w seconds",
		"-d, --discovered",
		"-m, --monitor",
		"-I seconds, --monitor-interval seconds",
		"-v, --verbose",
		"filter",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("help output missing %q, got:\n%v", want, output)
		}
	}
}

func TestIntegration_ExitCodeZero(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	tmpDir, cleanup := tempDirWithConfig(t)
	defer cleanup()
	cmd := exec.Command(bin, "--config", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gornstatus exited with error: %v\n%v", err, string(out))
	}
}

func TestIntegration_SIGINTCleanExit(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	tmpDir, cleanup := tempDirWithConfig(t)
	defer cleanup()
	cmd := exec.Command(bin, "--config", tmpDir, "-m", "-I", "10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGINT)
	err := cmd.Wait()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() > 1 {
			t.Errorf("expected clean exit, got: %v", err)
		}
	}
}

func TestIntegration_MonitorModeSIGINT(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	tmpDir, cleanup := tempDirWithConfig(t)
	defer cleanup()
	cmd := exec.Command(bin, "--config", tmpDir, "-m", "-I", "0.1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGINT)
	err := cmd.Wait()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() > 1 {
			t.Errorf("expected clean exit, got: %v", err)
		}
	}
}

func TestIntegration_VerboseStacking(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	out, err := exec.Command(bin, "-v", "-v", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornstatus -v -v --version failed: %v\n%v", err, string(out))
	}
	want := "gornstatus " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}
