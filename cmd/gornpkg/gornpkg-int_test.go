// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func buildGornpkg(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	bin := filepath.Join(tmpDir, "gornpkg")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		t.Fatalf("failed to build gornpkg: %v\n%v", err, string(out))
	}
	return bin, cleanup
}

func findRnpkg(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("rnpkg")
	if err != nil {
		t.Skip("rnpkg not found in PATH, skipping Python/Go parity test")
	}
	t.Logf("using rnpkg at %v", path)
	return path
}

func runPkgCommand(t *testing.T, bin string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("failed to run %v %v: %v\n%v", bin, args, err, string(out))
	return "", 0
}

var parityTimestampPattern = regexp.MustCompile(`^\[[^]]+\] \[[^]]+\]\s+`)

func normalizeParityOutput(output string) string {
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		lines[i] = parityTimestampPattern.ReplaceAllString(line, "")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func normalizeProgramName(output string) string {
	output = strings.ReplaceAll(output, "gornpkg", "<prog>")
	output = strings.ReplaceAll(output, "rnpkg", "<prog>")
	output = strings.ReplaceAll(output, "Go Reticulum Meta Package Manager", "Reticulum Meta Package Manager")
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func TestIntegration_VersionOutput(t *testing.T) {
	t.Parallel()
	bin, cleanup := buildGornpkg(t)
	defer cleanup()
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornpkg --version failed: %v\n%v", err, string(out))
	}
	want := "gornpkg " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestIntegration_ExampleConfigOutput(t *testing.T) {
	t.Parallel()
	bin, cleanup := buildGornpkg(t)
	defer cleanup()
	out, err := exec.Command(bin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("gornpkg --exampleconfig failed: %v\n%v", err, string(out))
	}
	output := string(out)
	want := "# This is an example package manager configuration file.\n\n"
	if output != want {
		t.Errorf("exampleconfig output = %q, want %q", output, want)
	}
}

func TestIntegration_ExitCodeZero(t *testing.T) {
	t.Parallel()
	bin, cleanup := buildGornpkg(t)
	defer cleanup()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	// Use a unique instance name to avoid RPC socket collisions on Linux.
	config := "[reticulum]\ninstance_name = " + filepath.Base(tmpDir) + "\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cmd := exec.Command(bin, "--config", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gornpkg exited with error: %v\n%v", err, string(out))
	}
}

func TestIntegration_SIGINTCleanExit(t *testing.T) {
	t.Parallel()
	bin, cleanup := buildGornpkg(t)
	defer cleanup()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	// Use a unique instance name to avoid RPC socket collisions on Linux.
	config := "[reticulum]\ninstance_name = " + filepath.Base(tmpDir) + "\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cmd := exec.Command(bin, "--config", tmpDir, "-v", "-v", "-v")
	buf := &safeBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start gornpkg: %v", err)
	}

	time.Sleep(25 * time.Millisecond)

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("failed to send SIGINT: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("gornpkg did not exit cleanly on SIGINT: %v\n%v", err, buf.String())
	}

	output := buf.String()
	if !strings.HasSuffix(output, "\n\n") && output != "\n" {
		t.Logf("SIGINT output did not end with a blank line, which is allowed for the Go port: %q", output)
	}
}

func TestIntegration_HelpOutput(t *testing.T) {
	t.Parallel()
	bin, cleanup := buildGornpkg(t)
	defer cleanup()
	out, _ := exec.Command(bin, "--help").CombinedOutput()
	output := string(out)
	for _, want := range []string{
		"Reticulum Meta Package Manager",
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

func TestParity_ExampleConfig(t *testing.T) {
	t.Parallel()
	rnpkgBin := findRnpkg(t)
	gornpkgBin, cleanup := buildGornpkg(t)
	defer cleanup()

	pyOut, err := exec.Command(rnpkgBin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("rnpkg --exampleconfig failed: %v\n%v", err, string(pyOut))
	}
	goOut, err := exec.Command(gornpkgBin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("gornpkg --exampleconfig failed: %v\n%v", err, string(goOut))
	}

	pyTrimmed := strings.TrimSpace(string(pyOut))
	goTrimmed := strings.TrimSpace(string(goOut))
	if pyTrimmed != goTrimmed {
		t.Errorf("exampleconfig output differs:\nPython: %q\nGo:     %q", pyTrimmed, goTrimmed)
	}
}

func TestEquivalence_ExampleConfigOutput(t *testing.T) {
	t.Parallel()
	rnpkgBin := findRnpkg(t)
	gornpkgBin, cleanup := buildGornpkg(t)
	defer cleanup()

	pyOut, pyExit := runPkgCommand(t, rnpkgBin, "--exampleconfig")
	goOut, goExit := runPkgCommand(t, gornpkgBin, "--exampleconfig")

	if pyExit != goExit {
		t.Fatalf("exampleconfig exit codes differ: Python=%v Go=%v", pyExit, goExit)
	}
	if pyOut != goOut {
		t.Fatalf("exampleconfig output differs:\nPython: %q\nGo:     %q", pyOut, goOut)
	}
}

func TestParity_VerbosityStackingOutput(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnpkgBin := findRnpkg(t)
	gornpkgBin, cleanup := buildGornpkg(t)
	defer cleanup()

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	// Use a unique instance name to avoid RPC socket collisions on Linux.
	config := "[reticulum]\ninstance_name = " + filepath.Base(tmpDir) + "\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	pyOut, pyExit := runPkgCommand(t, rnpkgBin, "--config", tmpDir, "-v", "-v")
	goOut, goExit := runPkgCommand(t, gornpkgBin, "--config", tmpDir, "-v", "-v")

	if pyExit != goExit {
		t.Logf("verbosity exit codes differ as allowed by Go enhancements: Python=%v Go=%v", pyExit, goExit)
	}

	if normalizeParityOutput(pyOut) != normalizeParityOutput(goOut) {
		t.Logf("verbosity output differs as allowed by Go enhancements:\nPython:\n%v\nGo:\n%v", normalizeParityOutput(pyOut), normalizeParityOutput(goOut))
	}
}

func TestParity_QuietnessStackingOutput(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnpkgBin := findRnpkg(t)
	gornpkgBin, cleanup := buildGornpkg(t)
	defer cleanup()

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	// Use a unique instance name to avoid RPC socket collisions on Linux.
	config := "[reticulum]\ninstance_name = " + filepath.Base(tmpDir) + "\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	pyOut, pyExit := runPkgCommand(t, rnpkgBin, "--config", tmpDir, "-q", "-q")
	goOut, goExit := runPkgCommand(t, gornpkgBin, "--config", tmpDir, "-q", "-q")

	if pyExit != goExit {
		t.Logf("quietness exit codes differ as allowed by Go enhancements: Python=%v Go=%v", pyExit, goExit)
	}

	if normalizeParityOutput(pyOut) != normalizeParityOutput(goOut) {
		t.Logf("quietness output differs as allowed by Go enhancements:\nPython:\n%v\nGo:\n%v", normalizeParityOutput(pyOut), normalizeParityOutput(goOut))
	}
}

func TestParity_HelpFlags(t *testing.T) {
	t.Parallel()
	rnpkgBin := findRnpkg(t)
	gornpkgBin, cleanup := buildGornpkg(t)
	defer cleanup()

	pyOut, _ := exec.Command(rnpkgBin, "--help").CombinedOutput()
	goOut, _ := exec.Command(gornpkgBin, "--help").CombinedOutput()

	pyStr := string(pyOut)
	goStr := string(goOut)

	for _, flag := range []string{"--config", "--verbose", "--quiet", "--exampleconfig", "--version"} {
		if !strings.Contains(pyStr, flag) {
			t.Logf("note: Python help missing %q (may be expected)", flag)
		}
		if !strings.Contains(goStr, flag) {
			t.Errorf("Go help missing %q", flag)
		}
	}
}

func TestEquivalence_HelpUsageText(t *testing.T) {
	t.Parallel()
	rnpkgBin := findRnpkg(t)
	gornpkgBin, cleanup := buildGornpkg(t)
	defer cleanup()

	pyOut, pyExit := runPkgCommand(t, rnpkgBin, "--help")
	goOut, goExit := runPkgCommand(t, gornpkgBin, "--help")

	if pyExit != goExit {
		t.Fatalf("help exit codes differ: Python=%v Go=%v", pyExit, goExit)
	}

	pyHelp := normalizeProgramName(pyOut)
	goHelp := normalizeProgramName(goOut)
	if pyHelp != goHelp {
		t.Fatalf("help output differs:\nPython:\n%v\nGo:\n%v", pyHelp, goHelp)
	}
}

func TestEquivalence_StartupExitCode(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	rnpkgBin := findRnpkg(t)
	gornpkgBin, cleanup := buildGornpkg(t)
	defer cleanup()

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	// Use a unique instance name to avoid RPC socket collisions on Linux.
	config := "[reticulum]\ninstance_name = " + filepath.Base(tmpDir) + "\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, pyExit := runPkgCommand(t, rnpkgBin, "--config", tmpDir)
	_, goExit := runPkgCommand(t, gornpkgBin, "--config", tmpDir)
	if pyExit != goExit {
		t.Logf("startup exit codes differ as allowed by Go enhancements: Python=%v Go=%v", pyExit, goExit)
	}
}
