// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"flag"
	"io"
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
	dir, err := os.MkdirTemp(baseDir, "gornstatus-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup
}

// tempDirWithConfig returns a temp directory pre-populated with a
// Reticulum config that uses a unique instance_name derived from
// the directory name. This prevents abstract-socket collisions
// when multiple test processes create Reticulum instances
// concurrently on Linux.
func tempDirWithConfig(t *testing.T) (string, func()) {
	t.Helper()
	dir, cleanup := tempDir(t)
	instanceName := filepath.Base(dir)
	config := "[reticulum]\nenable_transport = False\nshare_instance = Yes\ninstance_name = " + instanceName + "\n\n[logging]\nloglevel = 2\n"
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0o600); err != nil {
		cleanup()
		t.Fatalf("writeTestConfig: %v", err)
	}
	return dir, cleanup
}

func buildGornstatus(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, cleanup := tempDir(t)
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

func TestVersionOutput(t *testing.T) {
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

func TestHelpOutput(t *testing.T) {
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

func TestExitCodeZero(t *testing.T) {
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

func TestSIGINTCleanExit(t *testing.T) {
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

func TestMonitorModeSIGINT(t *testing.T) {
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

func TestVerboseStacking(t *testing.T) {
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

func TestAppFlags(t *testing.T) {
	t.Parallel()
	app := newApp()
	fs := flag.NewFlagSet("gornstatus", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.initFlags(fs)
	if err := fs.Parse([]string{"--config", "/tmp/config", "--all", "--json", "--verbose", "--monitor-interval", "2"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if app.configDir != "/tmp/config" {
		t.Fatalf("configDir = %q, want %q", app.configDir, "/tmp/config")
	}
	if !app.showAll {
		t.Fatal("showAll = false, want true")
	}
	if !app.jsonOutput {
		t.Fatal("jsonOutput = false, want true")
	}
	if app.verbose != 1 {
		t.Fatalf("verbose = %v, want %v", app.verbose, 1)
	}
	if app.monitorInterval != 2 {
		t.Fatalf("monitorInterval = %v, want 2", app.monitorInterval)
	}
}
