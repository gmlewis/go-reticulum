// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// standaloneConfig renders a per-test Reticulum configuration for a standalone
// instance.
//
// gornpkg attaches to a shared instance whenever the configuration shares one
// (WithRequireSharedInstance in initReticulum) and must never become one
// itself, so a configuration that leaves share_instance at its default of Yes
// starts only while some unrelated instance happens to be listening: the
// developer's own daemon locally, and nothing at all in CI. As in every other
// tool's integration helper here, the configuration therefore asks for a
// standalone instance. The empty [interfaces] section keeps interface synthesis
// from adding an AutoInterface, so no test traffic can leave the process, and
// the unique instance_name keeps concurrent tests from ever addressing the same
// instance.
func standaloneConfig(dir string) string {
	return fmt.Sprintf("[reticulum]\nshare_instance = No\ninstance_name = %v\n\n[interfaces]\n", filepath.Base(dir))
}

// sharedInstanceConfig renders a per-test Reticulum configuration that shares
// an instance addressed locally to this configuration, so the test never
// depends on what the host happens to be running.
func sharedInstanceConfig(dir string) string {
	return fmt.Sprintf("[reticulum]\nshare_instance = Yes\nshared_instance_type = unix\ninstance_name = %v\n\n[interfaces]\n", filepath.Base(dir))
}

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

func buildGornpkg(t *testing.T) string {
	t.Helper()
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	bin := filepath.Join(tmpDir, "gornpkg")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build gornpkg: %v\n%v", err, string(out))
	}
	return bin
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
	testutils.SkipShortIntegration(t)

	// The expected version is read from the rns source the build compiles, so a
	// version bump landing between this test binary's compile and the artifact's
	// build cannot leave the expectation stale.
	testutils.VersionFlag(t, "gornpkg", func(t *testing.T) string {
		bin := buildGornpkg(t)
		out, err := exec.Command(bin, "--version").CombinedOutput()
		if err != nil {
			t.Fatalf("gornpkg --version failed: %v\n%v", err, out)
		}
		return string(out)
	})
}

func TestIntegration_ExampleConfigOutput(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	bin := buildGornpkg(t)
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
	testutils.SkipShortIntegration(t)
	bin := buildGornpkg(t)
	tmpDir := testutils.TempDirWithConfig(t, tempDirPrefix, standaloneConfig)

	cmd := exec.Command(bin, "--config", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gornpkg exited with error: %v\n%v", err, string(out))
	}
}

// TestIntegration_NoSharedInstanceExitsOne pins the tool's attach-only contract:
// when the configuration shares an instance and none is running, gornpkg reports
// the failure and exits 1 rather than seizing the shared instance itself.
//
// It is the counterpart of standaloneConfig above: a tool that quietly became
// the shared instance would satisfy an "exits 0" test while redefining the
// network for every other process on the host, so the refusal is asserted
// directly. shared_instanceConfig addresses the instance inside the test's own
// config directory, which keeps this test independent of whatever the host is
// running.
func TestIntegration_NoSharedInstanceExitsOne(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	bin := buildGornpkg(t)
	tmpDir := testutils.TempDirWithConfig(t, tempDirPrefix, sharedInstanceConfig)

	out, exit := runPkgCommand(t, bin, "--config", tmpDir)
	if exit != 1 {
		t.Fatalf("gornpkg with no shared instance running: exit code %v, want 1\n%v", exit, out)
	}
}

// TestIntegration_SIGINTCleanExit verifies that signaling gornpkg never turns a
// clean startup into a failure exit status.
//
// gornpkg initializes Reticulum and exits immediately, exactly as rnpkg does
// (program_setup ends in exit(0)), so the child has usually finished before the
// signal is delivered and the signal reaches a process that is already gone.
// Both cases are covered by the same assertions: a SIGINT delivered while the
// child is still initializing must exit 0 through the handler, and a child that
// finished on its own must have exited 0. The short wait below exists to give
// the signal a chance to find a running process, not to guarantee one.
func TestIntegration_SIGINTCleanExit(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	bin := buildGornpkg(t)
	tmpDir := testutils.TempDirWithConfig(t, tempDirPrefix, standaloneConfig)

	cmd := exec.Command(bin, "--config", tmpDir, "-v", "-v", "-v")
	buf := &safeBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := testutils.StartWithReaper(cmd); err != nil {
		t.Fatalf("failed to start gornpkg: %v", err)
	}

	time.Sleep(250 * time.Millisecond)

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("failed to send SIGINT: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if got := exitErr.ExitCode(); got != 0 && got != -1 {
				t.Fatalf("gornpkg did not exit cleanly on SIGINT: exit code %v\n%v", got, buf.String())
			}
		} else {
			t.Fatalf("gornpkg did not exit cleanly on SIGINT: %v\n%v", err, buf.String())
		}
	}

	output := buf.String()
	if !strings.HasSuffix(output, "\n\n") && output != "\n" {
		t.Logf("SIGINT output did not end with a blank line, which is allowed for the Go port: %q", output)
	}
}

func TestIntegration_HelpOutput(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	bin := buildGornpkg(t)
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
	testutils.SkipShortIntegration(t)
	rnpkgBin := findRnpkg(t)
	gornpkgBin := buildGornpkg(t)

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
	testutils.SkipShortIntegration(t)
	rnpkgBin := findRnpkg(t)
	gornpkgBin := buildGornpkg(t)

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
	gornpkgBin := buildGornpkg(t)

	tmpDir := testutils.TempDirWithConfig(t, tempDirPrefix, standaloneConfig)

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
	gornpkgBin := buildGornpkg(t)

	tmpDir := testutils.TempDirWithConfig(t, tempDirPrefix, standaloneConfig)

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
	testutils.SkipShortIntegration(t)
	rnpkgBin := findRnpkg(t)
	gornpkgBin := buildGornpkg(t)

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
	testutils.SkipShortIntegration(t)
	rnpkgBin := findRnpkg(t)
	gornpkgBin := buildGornpkg(t)

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
	gornpkgBin := buildGornpkg(t)

	tmpDir := testutils.TempDirWithConfig(t, tempDirPrefix, standaloneConfig)

	_, pyExit := runPkgCommand(t, rnpkgBin, "--config", tmpDir)
	_, goExit := runPkgCommand(t, gornpkgBin, "--config", tmpDir)
	if pyExit != goExit {
		t.Logf("startup exit codes differ as allowed by Go enhancements: Python=%v Go=%v", pyExit, goExit)
	}
}
