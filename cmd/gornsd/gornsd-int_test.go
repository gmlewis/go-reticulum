// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux
// +build integration,linux

package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	exec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

type cliOutcome struct {
	stdout   string
	stderr   string
	exitCode int
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}
	return root
}

func buildGornsdBinary(t *testing.T) string {
	t.Helper()
	buildDir, cleanup := testutils.TempDir(t, "gornsd-int-bin-")
	t.Cleanup(cleanup)

	binaryPath := filepath.Join(buildDir, "gornsd")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/gornsd")
	cmd.Dir = repoRoot(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build failed: %v\nstdout=%v\nstderr=%v", err, stdout.String(), stderr.String())
	}
	return binaryPath
}

func writeGornsdUDPConfig(t *testing.T, dir string, shareInstance string, loglevel int, listenPort int, forwardPort int) {
	t.Helper()
	instanceName := filepath.Base(dir)
	config := fmt.Sprintf(`[reticulum]
share_instance = %v
instance_name = %v

[logging]
loglevel = %v

[interfaces]
  [[UDP Interface]]
    type = UDPInterface
    enabled = Yes
    listen_ip = 127.0.0.1
    listen_port = %v
	forward_ip = 127.0.0.1
	forward_port = %v
`, shareInstance, instanceName, loglevel, listenPort, forwardPort)
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config error: %v", err)
	}
}

func reserveUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveUDPPort: %v", err)
	}
	defer func() { _ = conn.Close() }()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

func reserveTCPPortForIntegration(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserveTCPPortForIntegration: %v", err)
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func startGornsdBinary(t *testing.T, binaryPath string, args ...string) (*exec.Cmd, *lockedBuffer, *lockedBuffer) {
	t.Helper()
	cmd := exec.Command(binaryPath, args...)
	var stdout lockedBuffer
	var stderr lockedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start gornsd binary: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	return cmd, &stdout, &stderr
}

func waitForCombinedOutput(t *testing.T, stdout, stderr *lockedBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(stdout.String()+stderr.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("combined output did not contain %q\nstdout=%v\nstderr=%v", want, stdout.String(), stderr.String())
}

func waitForFileContainsIntegration(t *testing.T, path string, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%v did not contain %q", path, want)
}

func waitForProcessExit(t *testing.T, cmd *exec.Cmd) int {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		if err == nil {
			return 0
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		t.Fatalf("process wait failed: %v", err)
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("timeout waiting for gornsd to exit")
	}
	return -1
}

func runGornsdOutcome(t *testing.T, args ...string) cliOutcome {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "./cmd/gornsd"}, args...)...)
	cmd.Dir = filepath.Clean(filepath.Join("..", ".."))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return cliOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitErr.ExitCode()}
		}
		t.Fatalf("gornsd command failed: %v", err)
	}
	return cliOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: 0}
}

func runRnsdOutcome(t *testing.T, args ...string) cliOutcome {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", "original-reticulum-repo"))
	if err != nil {
		t.Fatalf("failed to resolve original repo root: %v", err)
	}
	cmd := exec.Command("python3", append([]string{"RNS/Utilities/rnsd.py"}, args...)...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PYTHONPATH="+repoRoot)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return cliOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitErr.ExitCode()}
		}
		t.Fatalf("rnsd command failed: %v", err)
	}
	return cliOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: 0}
}

func normalizeHelpOutput(text string) string {
	text = strings.ReplaceAll(text, "gornsd", "rnsd")
	text = strings.ReplaceAll(text, "rnsd.py", "rnsd")
	text = strings.ReplaceAll(text, "Go Reticulum Network Stack Daemon", "Reticulum Network Stack Daemon")
	return strings.Join(strings.Fields(text), " ")
}

func normalizeMultilineWhitespace(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func TestGornsdHelpParity(t *testing.T) {
	t.Parallel()
	got := runGornsdOutcome(t, "--help")
	want := runRnsdOutcome(t, "--help")
	if got.exitCode != want.exitCode {
		t.Fatalf("exit code mismatch: got %v want %v", got.exitCode, want.exitCode)
	}
	if normalizeHelpOutput(got.stderr) != normalizeHelpOutput(want.stdout) {
		t.Fatalf("usage mismatch:\n--- got ---\n%v--- want ---\n%v", got.stderr, want.stdout)
	}
	if got.stdout != "" || want.stderr != "" {
		t.Fatalf("unexpected extra output: got stdout=%q want stderr=%q", got.stdout, want.stderr)
	}
}

func TestGornsdExampleConfigParity(t *testing.T) {
	t.Parallel()
	got := runGornsdOutcome(t, "--exampleconfig")
	want := runRnsdOutcome(t, "--exampleconfig")
	if got.exitCode != want.exitCode {
		t.Fatalf("exit code mismatch: got %v want %v", got.exitCode, want.exitCode)
	}
	if normalizeMultilineWhitespace(got.stderr) != normalizeMultilineWhitespace(want.stderr) {
		t.Fatalf("stderr mismatch:\n--- got ---\n%v--- want ---\n%v", got.stderr, want.stderr)
	}
	if normalizeMultilineWhitespace(got.stdout) != normalizeMultilineWhitespace(want.stdout) {
		t.Fatalf("stdout mismatch:\n--- got ---\n%v--- want ---\n%v", got.stdout, want.stdout)
	}
}

func TestGornsdVersionOutputs(t *testing.T) {
	t.Parallel()
	got := runGornsdOutcome(t, "--version")
	want := runRnsdOutcome(t, "--version")
	if got.exitCode != 0 || want.exitCode != 0 {
		t.Fatalf("version command exit codes: got %v want %v", got.exitCode, want.exitCode)
	}
	if got.stderr != "" || want.stderr != "" {
		t.Fatalf("version stderr mismatch: got %q want %q", got.stderr, want.stderr)
	}
	if got.stdout != "gornsd 0.1.0\n" {
		t.Fatalf("gornsd stdout = %q, want %q", got.stdout, "gornsd 0.1.0\n")
	}
	if want.stdout != "rnsd 1.1.4\n" {
		t.Fatalf("rnsd stdout = %q, want %q", want.stdout, "rnsd 1.1.4\n")
	}
}

func TestGornsdUnknownFlagExitCode2(t *testing.T) {
	t.Parallel()
	binaryPath := buildGornsdBinary(t)
	cmd := exec.Command(binaryPath, "--bogus-flag")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected unknown flag to fail")
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
		t.Fatalf("exit code = %v, want 2", err)
	}
	if !strings.Contains(stderr.String(), "bogus-flag") {
		t.Fatalf("stderr = %q, want flag name", stderr.String())
	}
}

func TestGornsdStartupAndSIGTERM(t *testing.T) {
	t.Parallel()
	configDir, cleanup := testutils.TempDir(t, "gornsd-int-startup-")
	t.Cleanup(cleanup)
	writeGornsdConfig(t, configDir, "No", 4)

	binaryPath := buildGornsdBinary(t)
	cmd, stdout, stderr := startGornsdBinary(t, binaryPath, "--config", configDir)
	waitForCombinedOutput(t, stdout, stderr, "Started gornsd version")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed to signal SIGTERM: %v", err)
	}
	if got := waitForProcessExit(t, cmd); got != 0 {
		t.Fatalf("exit code = %v, want 0", got)
	}
	if got := strings.Count(stdout.String()+stderr.String(), "Started gornsd version"); got != 1 {
		t.Fatalf("startup notice count = %v, want 1", got)
	}
}

func TestGornsdSIGINTBlankLine(t *testing.T) {
	t.Parallel()
	configDir, cleanup := testutils.TempDir(t, "gornsd-int-sigint-")
	t.Cleanup(cleanup)
	writeGornsdConfig(t, configDir, "No", 4)

	binaryPath := buildGornsdBinary(t)
	cmd, stdout, stderr := startGornsdBinary(t, binaryPath, "--config", configDir)
	waitForCombinedOutput(t, stdout, stderr, "Started gornsd version")
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("failed to signal SIGINT: %v", err)
	}
	if got := waitForProcessExit(t, cmd); got != 0 {
		t.Fatalf("exit code = %v, want 0", got)
	}
	if out := stdout.String(); len(out) == 0 || out[len(out)-1] != '\n' {
		t.Fatalf("stdout = %q, want trailing blank line", out)
	}
}

func TestGornsdServiceModeLogsToFile(t *testing.T) {
	t.Parallel()
	configDir, cleanup := testutils.TempDir(t, "gornsd-int-service-")
	t.Cleanup(cleanup)
	writeGornsdConfig(t, configDir, "No", 4)

	binaryPath := buildGornsdBinary(t)
	cmd, stdout, stderr := startGornsdBinary(t, binaryPath, "--config", configDir, "-s")
	waitForFileContainsIntegration(t, filepath.Join(configDir, "logfile"), "Started gornsd version")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed to signal SIGTERM: %v", err)
	}
	if got := waitForProcessExit(t, cmd); got != 0 {
		t.Fatalf("exit code = %v, want 0", got)
	}
	if stdout.String() != "" || stderr.String() != "" {
		t.Fatalf("expected no stdout/stderr in service mode, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestGornsdVerbosityIncreases(t *testing.T) {

	configDir, cleanup := testutils.TempDir(t, "gornsd-int-verbose-")
	t.Cleanup(cleanup)
	listenPort := reserveUDPPort(t)
	forwardPort := reserveUDPPort(t)
	writeGornsdUDPConfig(t, configDir, "No", 4, listenPort, forwardPort)

	binaryPath := buildGornsdBinary(t)
	cmd, stdout, stderr := startGornsdBinary(t, binaryPath, "--config", configDir, "-v", "-v")
	waitForCombinedOutput(t, stdout, stderr, "[Debug]")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed to signal SIGTERM: %v", err)
	}
	if got := waitForProcessExit(t, cmd); got != 0 {
		t.Fatalf("exit code = %v, want 0", got)
	}
	if !strings.Contains(stdout.String()+stderr.String(), "Started UDP interface") {
		t.Fatalf("expected UDP interface startup info in output, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestGornsdQuietDecreases(t *testing.T) {
	t.Parallel()
	configDir, cleanup := testutils.TempDir(t, "gornsd-int-quiet-")
	t.Cleanup(cleanup)
	listenPort := reserveUDPPort(t)
	forwardPort := reserveUDPPort(t)
	writeGornsdUDPConfig(t, configDir, "No", 4, listenPort, forwardPort)

	binaryPath := buildGornsdBinary(t)
	cmd, stdout, stderr := startGornsdBinary(t, binaryPath, "--config", configDir, "-q")
	waitForCombinedOutput(t, stdout, stderr, "Started gornsd version")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed to signal SIGTERM: %v", err)
	}
	if got := waitForProcessExit(t, cmd); got != 0 {
		t.Fatalf("exit code = %v, want 0", got)
	}
	output := stdout.String() + stderr.String()
	if strings.Contains(output, "[Info]    ") || strings.Contains(output, "Started UDP interface") {
		t.Fatalf("expected info-level messages to be suppressed, got output=%q", output)
	}
}

func TestGornsdSharedInstanceWarning(t *testing.T) {
	t.Parallel()
	configDir, cleanup := testutils.TempDir(t, "gornsd-int-shared-")
	t.Cleanup(cleanup)
	sharedPort := reserveTCPPortForIntegration(t)
	rpcPort := reserveTCPPortForIntegration(t)
	config := fmt.Sprintf(`[reticulum]
instance_name = gornsd-shared-instance
share_instance = Yes
shared_instance_type = tcp
shared_instance_port = %v
instance_control_port = %v

[logging]
loglevel = 4

[interfaces]
`, sharedPort, rpcPort)
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("write config error: %v", err)
	}

	sharedLogger := rns.NewLogger()
	sharedLogger.SetLogDest(rns.LogCallback)
	sharedLogger.SetLogCallback(func(string) {})
	sharedTS := rns.NewTransportSystem(sharedLogger)
	shared, err := rns.NewReticulumWithLogger(sharedTS, configDir, sharedLogger)
	if err != nil {
		t.Fatalf("failed to start shared instance: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := shared.Close(); closeErr != nil {
			t.Fatalf("shared.Close error: %v", closeErr)
		}
	})

	binaryPath := buildGornsdBinary(t)
	cmd, stdout, stderr := startGornsdBinary(t, binaryPath, "--config", configDir)
	waitForCombinedOutput(t, stdout, stderr, "connected to another shared local instance")
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("failed to signal SIGTERM: %v", err)
	}
	if got := waitForProcessExit(t, cmd); got != 0 {
		t.Fatalf("exit code = %v, want 0", got)
	}
}

func TestGornsdInteractiveModeREPL(t *testing.T) {
	t.Parallel()
	configDir, cleanup := testutils.TempDir(t, "gornsd-int-shared-")
	t.Cleanup(cleanup)
	writeGornsdConfig(t, configDir, "No", 4)

	binaryPath := buildGornsdBinary(t)
	cmd := exec.Command(binaryPath, "--config", configDir, "-i")
	cmd.Stdin = strings.NewReader("version\nquit\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("interactive mode exited %v:\nstdout=%v\nstderr=%v", exitErr.ExitCode(), stdout.String(), stderr.String())
		}
		t.Fatalf("interactive mode failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "gornsd 0.1.0") {
		t.Fatalf("stdout = %q, want version output", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Goodbye.") {
		t.Fatalf("stdout = %q, want goodbye output", stdout.String())
	}
}
