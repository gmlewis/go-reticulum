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
	dir, err := os.MkdirTemp(baseDir, "gornir-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup
}

func runGornir(t *testing.T, args ...string) (string, error) {
	t.Helper()
	fullArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", fullArgs...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestHelpOutput(t *testing.T) {
	t.Parallel()
	out, err := runGornir(t, "--help")
	// --help causes flag.Parse to exit with code 0 via flag.Usage
	// but Go's flag package exits with code 2 for -help by default
	// unless we set flag.Usage. Either way, check output content.
	_ = err
	for _, want := range []string{
		"Reticulum Distributed Identity Resolver",
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
	out, err := runGornir(t, "--exampleconfig")
	if err != nil {
		t.Fatalf("gornir --exampleconfig failed: %v\n%v", err, out)
	}
	for _, want := range []string{
		"example Reticulum config file",
		"[reticulum]",
		"enable_transport",
		"[logging]",
		"[interfaces]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exampleconfig output missing %q", want)
		}
	}
}

func TestExitCodeZero(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	out, err := runGornir(t, "--config", tmpDir)
	if err != nil {
		t.Fatalf("gornir exited with error: %v\n%v", err, out)
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

func TestVerboseStacking(t *testing.T) {
	t.Parallel()
	out, err := runGornir(t, "-v", "-v", "--version")
	if err != nil {
		t.Fatalf("gornir -v -v --version failed: %v\n%v", err, out)
	}
	want := "gornir " + rns.VERSION
	got := strings.TrimSpace(out)
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestLongFormParserAliases(t *testing.T) {
	t.Parallel()
	out, err := runGornir(t, "--verbose", "--quiet", "--exampleconfig")
	if err != nil {
		t.Fatalf("gornir --verbose --quiet --exampleconfig failed: %v\n%v", err, out)
	}
	for _, want := range []string{"example Reticulum config file", "[reticulum]", "[logging]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("parser alias output missing %q: %v", want, out)
		}
	}
}

func TestVersionOutput(t *testing.T) {
	t.Parallel()
	out, err := runGornir(t, "--version")
	if err != nil {
		t.Fatalf("gornir --version failed: %v\n%v", err, out)
	}
	want := "gornir " + rns.VERSION
	got := strings.TrimSpace(out)
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestAppFlags(t *testing.T) {
	t.Parallel()
	app := newApp()
	fs := flag.NewFlagSet("gornir", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	app.initFlags(fs)
	if err := fs.Parse([]string{"--verbose", "--quiet", "--exampleconfig"}); err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if app.verbose != 1 {
		t.Fatalf("verbose = %v, want %v", app.verbose, 1)
	}
	if app.quiet != 1 {
		t.Fatalf("quiet = %v, want %v", app.quiet, 1)
	}
	if !app.exampleConfig {
		t.Fatal("exampleConfig = false, want true")
	}
}
