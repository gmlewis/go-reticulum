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

func tempDir(t *testing.T) string {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "gornir-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func buildGornir(t *testing.T) string {
	t.Helper()
	tmpDir := tempDir(t)
	bin := filepath.Join(tmpDir, "gornir")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build gornir: %v\n%v", err, string(out))
	}
	return bin
}

func TestHelpOutput(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	out, err := exec.Command(bin, "--help").CombinedOutput()
	// --help causes flag.Parse to exit with code 0 via flag.Usage
	// but Go's flag package exits with code 2 for -help by default
	// unless we set flag.Usage. Either way, check output content.
	_ = err
	output := string(out)
	for _, want := range []string{
		"Reticulum Distributed Identity Resolver",
		"--config",
		"-v, --verbose",
		"-q, --quiet",
		"--exampleconfig",
		"--version",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("help output missing %q, got:\n%v", want, output)
		}
	}
}

func TestCounter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		calls int
		want  int
	}{
		{"zero", 0, 0},
		{"one", 1, 1},
		{"three", 3, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var c counter
			for i := 0; i < tc.calls; i++ {
				if err := c.Set("true"); err != nil {
					t.Fatalf("Set failed: %v", err)
				}
			}
			if int(c) != tc.want {
				t.Errorf("counter = %v, want %v", int(c), tc.want)
			}
		})
	}
}

func TestCounterIsBoolFlag(t *testing.T) {
	t.Parallel()
	var c counter
	if !c.IsBoolFlag() {
		t.Error("IsBoolFlag() = false, want true")
	}
}

func TestCounterString(t *testing.T) {
	t.Parallel()
	var c counter
	if c.String() != "0" {
		t.Errorf("String() = %q, want %q", c.String(), "0")
	}
	c = 5
	if c.String() != "5" {
		t.Errorf("String() = %q, want %q", c.String(), "5")
	}
}

func TestExampleConfig(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	out, err := exec.Command(bin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("gornir --exampleconfig failed: %v\n%v", err, string(out))
	}
	output := string(out)
	for _, want := range []string{
		"example Reticulum config file",
		"[reticulum]",
		"enable_transport",
		"[logging]",
		"[interfaces]",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("exampleconfig output missing %q", want)
		}
	}
}

func TestExitCodeZero(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	tmpDir := tempDir(t)
	cmd := exec.Command(bin, "--config", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gornir exited with error: %v\n%v", err, string(out))
	}
}

func TestSIGINTCleanExit(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	tmpDir := tempDir(t)
	cmd := exec.Command(bin, "--config", tmpDir, "-v", "-v", "-v")
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

func TestVerboseStacking(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	// -v -v --version should not error — it should print version and exit
	out, err := exec.Command(bin, "-v", "-v", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornir -v -v --version failed: %v\n%v", err, string(out))
	}
	want := "gornir " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestVersionOutput(t *testing.T) {
	t.Parallel()
	bin := buildGornir(t)
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornir --version failed: %v\n%v", err, string(out))
	}
	want := "gornir " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}
