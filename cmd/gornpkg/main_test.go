// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func tempDir(t *testing.T) (string, func()) {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "gornpkg-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup
}

func runGornpkg(t *testing.T, args ...string) (string, error) {
	t.Helper()
	fullArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", fullArgs...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestVersionOutput(t *testing.T) {
	t.Parallel()
	out, err := runGornpkg(t, "--version")
	if err != nil {
		t.Fatalf("gornpkg --version failed: %v\n%v", err, out)
	}
	want := "gornpkg " + rns.VERSION
	got := strings.TrimSpace(out)
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestHelpOutput(t *testing.T) {
	t.Parallel()
	out, err := runGornpkg(t, "--help")
	_ = err
	for _, want := range []string{
		"Reticulum Meta Package Manager",
		"--config",
		"-v, --verbose",
		"-q, --quiet",
		"--exampleconfig",
		"--version",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q, got:\n%v", want, out)
		}
	}
}

func TestExampleConfig(t *testing.T) {
	t.Parallel()
	out, err := runGornpkg(t, "--exampleconfig")
	if err != nil {
		t.Fatalf("gornpkg --exampleconfig failed: %v\n%v", err, out)
	}
	want := "# This is an example package manager configuration file.\n\n"
	if out != want {
		t.Errorf("exampleconfig output = %q, want %q", out, want)
	}
}

func TestVerboseStacking(t *testing.T) {
	t.Parallel()
	out, err := runGornpkg(t, "-v", "-v", "--version")
	if err != nil {
		t.Fatalf("gornpkg -v -v --version failed: %v\n%v", err, out)
	}
	want := "gornpkg " + rns.VERSION
	got := strings.TrimSpace(out)
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestExitCodeZero(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	out, err := runGornpkg(t, "--config", tmpDir)
	if err != nil {
		t.Fatalf("gornpkg exited with error: %v\n%v", err, out)
	}
}

func TestProgramSetupUsesVerbosityMinusQuietness(t *testing.T) {
	originalLevel := rns.GetLogLevel()
	originalDest := rns.GetLogDest()
	t.Cleanup(func() {
		rns.SetLogLevel(originalLevel)
		rns.SetLogDest(originalDest)
	})

	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	var capturedLevel int
	var capturedDest int
	var capturedConfigDir string
	if err := programSetup(tmpDir, 3, 1, func(ts rns.Transport, configDir string) (*rns.Reticulum, error) {
		capturedLevel = rns.GetLogLevel()
		capturedDest = rns.GetLogDest()
		capturedConfigDir = configDir
		return &rns.Reticulum{}, nil
	}); err != nil {
		t.Fatalf("programSetup returned error: %v", err)
	}
	if got, want := capturedLevel, 2; got != want {
		t.Fatalf("log level = %v, want %v", got, want)
	}
	if got, want := capturedDest, rns.LogStdout; got != want {
		t.Fatalf("log dest = %v, want %v", got, want)
	}
	if capturedConfigDir != tmpDir {
		t.Fatalf("config dir = %q, want %q", capturedConfigDir, tmpDir)
	}
}

func TestProgramSetupForwardsConfigDirAndClosesReticulum(t *testing.T) {
	originalLevel := rns.GetLogLevel()
	originalDest := rns.GetLogDest()
	t.Cleanup(func() {
		rns.SetLogLevel(originalLevel)
		rns.SetLogDest(originalDest)
	})

	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	if err := programSetup(tmpDir, 0, 0, rns.NewReticulum); err != nil {
		t.Fatalf("first programSetup returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "config")); err != nil {
		t.Fatalf("expected config file to be created in %v: %v", tmpDir, err)
	}
	if err := programSetup(tmpDir, 0, 0, rns.NewReticulum); err != nil {
		t.Fatalf("second programSetup returned error: %v", err)
	}
}

func TestSIGINTCleanExit(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	cmd := exec.Command("go", "run", ".", "--config", tmpDir, "-v", "-v", "-v")
	cmd.Dir = "."
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGINT)
	err := cmd.Wait()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() > 1 {
			t.Errorf("expected clean exit, got: %v", err)
		}
	}
}
