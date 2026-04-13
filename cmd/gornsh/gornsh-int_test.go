// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
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

var versionLineRE = regexp.MustCompile(`^[^[:space:]]+\s+[^[:space:]]+$`)

var gornshBinaryPath string

func TestMain(m *testing.M) {
	// This entire suite will be skipped if `-short` is used.
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}

	binDir, cleanup := testutils.TempDirMain("gornsh-bin-")
	defer func() {
		cleanup()
		out, err := exec.Command("/usr/bin/pkill", "-f", binDir).CombinedOutput()
		if err != nil {
			log.Fatalf("pkill -f %q failed: %v\n%s", binDir, err, out)
		}
	}()

	gornshBinaryPath = filepath.Join(binDir, "gornsh")
	build := exec.Command("go", "build", "-o", gornshBinaryPath, ".")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("failed to build gornsh binary: %v\n", err)
	}

	os.Exit(m.Run())
}

func TestIntegrationScaffoldHelpers(t *testing.T) {
	t.Parallel()
	if got := getRnshPythonPath(); got == "" {
		t.Fatal("getRnshPythonPath() returned empty path")
	}
}

func TestIntegrationVersionOutputFormatParity(t *testing.T) {
	t.Parallel()
	pythonBin := getRnshBinaryPath(t)
	gornshBin := getGornshBinaryPath(t)
	pythonOut, err := exec.Command(pythonBin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("rnsh --version failed: %v\n%v", err, string(pythonOut))
	}
	goOut, err := exec.Command(gornshBin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornsh --version failed: %v\n%v", err, string(goOut))
	}

	pythonLine := strings.TrimSpace(string(pythonOut))
	goLine := strings.TrimSpace(string(goOut))
	if !versionLineRE.MatchString(pythonLine) {
		t.Fatalf("rnsh version output %q does not match version-line format", pythonLine)
	}
	if !versionLineRE.MatchString(goLine) {
		t.Fatalf("gornsh version output %q does not match version-line format", goLine)
	}
	if strings.Fields(pythonLine)[0] != "rnsh" {
		t.Fatalf("rnsh version prefix = %q, want rnsh", strings.Fields(pythonLine)[0])
	}
	if strings.Fields(goLine)[0] != "gornsh" {
		t.Fatalf("gornsh version prefix = %q, want gornsh", strings.Fields(goLine)[0])
	}
}

func TestIntegrationListenPrintIdentityOutputFormatParity(t *testing.T) {
	t.Parallel()
	pythonBin := getRnshBinaryPath(t)
	gornshBin := getGornshBinaryPath(t)
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	prepareGornshConfig(t, configDir)

	pythonOut, err := exec.Command(pythonBin, "--config", configDir, "-l", "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("rnsh -l -p failed: %v\n%v", err, string(pythonOut))
	}
	goOut, err := exec.Command(gornshBin, "--config", configDir, "-l", "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("gornsh -l -p failed: %v\n%v", err, string(goOut))
	}

	pythonText := string(pythonOut)
	goText := string(goOut)
	for _, output := range []struct {
		name string
		text string
	}{
		{name: "rnsh", text: pythonText},
		{name: "gornsh", text: goText},
	} {
		if !strings.Contains(output.text, "Identity     : ") {
			t.Fatalf("%v output missing identity prefix:\n%v", output.name, output.text)
		}
		if !strings.Contains(output.text, "Listening on : ") {
			t.Fatalf("%v output missing destination prefix:\n%v", output.name, output.text)
		}
		if !strings.Contains(output.text, "<") || !strings.Contains(output.text, ">") {
			t.Fatalf("%v output missing wrapped destination hash:\n%v", output.name, output.text)
		}
	}
}

func TestIntegrationPrintIdentityOutputFormatParity(t *testing.T) {
	t.Parallel()
	pythonBin := getRnshBinaryPath(t)
	gornshBin := getGornshBinaryPath(t)
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	prepareGornshConfig(t, configDir)

	pythonOut, err := exec.Command(pythonBin, "--config", configDir, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("rnsh -p failed: %v\n%v", err, string(pythonOut))
	}
	goOut, err := exec.Command(gornshBin, "--config", configDir, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("gornsh -p failed: %v\n%v", err, string(goOut))
	}

	pythonLine := firstLineWithPrefix(string(pythonOut), "Identity     : ")
	goLine := firstLineWithPrefix(string(goOut), "Identity     : ")
	if pythonLine == "" {
		t.Fatalf("rnsh -p output missing identity line:\n%v", string(pythonOut))
	}
	if goLine == "" {
		t.Fatalf("gornsh -p output missing identity line:\n%v", string(goOut))
	}
	if !strings.HasPrefix(pythonLine, "Identity     : ") {
		t.Fatalf("rnsh identity line has wrong prefix: %q", pythonLine)
	}
	if !strings.HasPrefix(goLine, "Identity     : ") {
		t.Fatalf("gornsh identity line has wrong prefix: %q", goLine)
	}
	if strings.TrimSpace(strings.TrimPrefix(pythonLine, "Identity     : ")) != strings.TrimSpace(strings.TrimPrefix(goLine, "Identity     : ")) {
		t.Fatalf("identity values differ:\nrnsh:   %q\ngornsh: %q", pythonLine, goLine)
	}
}

func TestIntegrationGoListenerGoInitiatorEcho(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	configDir, cleanup := testutils.TempDir(t, "gornsh-go-go-")
	defer cleanup()

	instanceName := "gornsh-go-go-" + filepath.Base(configDir)
	prepareGornshConfigWithInstance(t, configDir, instanceName, 0, 0)

	listener := startGornshListener(t, configDir)
	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	// Wait for the shared instance transport to propagate the listener's path.
	// Do NOT use waitForGornshConnection here — the probe creates a real session
	// on the listener, leaving stale PTY state that corrupts the actual test command.
	time.Sleep(5 * time.Second)

	// Initiator call should be rock-solid with enough timeout
	// When using shared instance, we MUST use the same configDir
	output, exitCode := runGornshCommand(t, configDir, 60*time.Second, "--timeout", "30", "-T", readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", exitCode, output, listener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("initiator output %q missing hello\nlistener output:\n%v", output, listener.output())
	}
}

// TODO E06: End-to-end echo test (Python listener ↔ Go initiator).
// Start `rnsh -l --no-auth` as a subprocess; wait for readiness line; connect with Go initiator;
// verify stdout and exit code.
func TestIntegrationPythonListenerGoInitiatorEcho(t *testing.T) {

	testutils.SkipShortIntegration(t)
	// Set up temporary directory for Python listener config
	configDir, cleanup := testutils.TempDir(t, "gornsh-py-go-")
	defer cleanup()

	instanceName := "gornsh-py-go-" + filepath.Base(configDir)
	prepareGornshConfigWithInstance(t, configDir, instanceName, 0, 0)

	// Start Python listener as subprocess
	pythonListener := startPythonListener(t, configDir, instanceName, 0, 0)
	readyHash := pythonListener.hash()
	if readyHash == "" {
		t.Fatal("Python listener hash is empty")
	}

	// Wait for the shared instance transport to propagate the listener's path.
	// Do NOT use waitForGornshConnection here — the probe creates a real session
	// on the listener, leaving stale PTY state that corrupts the actual test command.
	time.Sleep(5 * time.Second)

	// Initiator call should be rock-solid with enough timeout
	// When using shared instance, we MUST use the same configDir
	output, exitCode := runGornshCommand(t, configDir, 60*time.Second, "--timeout", "30", "-T", readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", exitCode, output, pythonListener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("initiator output %q missing hello\nlistener output:\n%v", output, pythonListener.output())
	}
}

func TestIntegrationGoListenerPythonInitiatorEcho(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	// Listener config
	lConfigDir, cleanup1 := testutils.TempDir(t, "gornsh-go-listen-")
	defer cleanup1()
	// Initiator config
	iConfigDir, cleanup2 := testutils.TempDir(t, "gornsh-py-init-")
	defer cleanup2()

	instanceName := "gornsh-go-py-" + filepath.Base(lConfigDir)

	// Go listener config
	prepareGornshConfigWithInstance(t, lConfigDir, instanceName, 0, 0)
	// Python initiator config
	prepareGornshConfigWithInstance(t, iConfigDir, instanceName, 0, 0)

	listener := startGornshListener(t, lConfigDir)
	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	// Wait for the shared instance transport to propagate the listener's path.
	// Do NOT use waitForGornshConnection here — the probe creates a real session
	// on the listener, leaving stale PTY state that corrupts the actual test command.
	time.Sleep(5 * time.Second)

	// Initiator call (Python)
	output, exitCode := runRnshCommand(t, iConfigDir, 60*time.Second, "--timeout", "30", "-T", readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("Python initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", exitCode, output, listener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("Python initiator output %q missing hello\nlistener output:\n%v", output, listener.output())
	}
}

func runRnshCommand(t *testing.T, configDir string, timeout time.Duration, args ...string) (string, int) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fullArgs := append([]string{"-m", "rnsh.rnsh", "--config", configDir}, args...)
	cmd := exec.CommandContext(ctx, "python3", fullArgs...)
	cmd.Stdin = strings.NewReader("")
	cmd.Env = gornshIntegrationEnv("")

	t.Logf("Running rnsh command: python3 %v", fullArgs)
	out, err := cmd.CombinedOutput()
	t.Logf("Command finished. Output: %q, Error: %v", string(out), err)
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	return string(out), -1
}

func firstLineWithPrefix(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

type gornshListenerProcess struct {
	cmd     *exec.Cmd
	stdout  *bytes.Buffer
	value   string
	hashMu  sync.Mutex
	readyCh chan struct{}
	waitCh  chan error
}

func TestIntegrationAllowedIdentityEnforcement(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	// Listener config
	lConfigDir, cleanup1 := testutils.TempDir(t, "gornsh-go-allowed-listen-")
	defer cleanup1()
	// Initiator config
	iConfigDir, cleanup2 := testutils.TempDir(t, "gornsh-go-allowed-init-")
	defer cleanup2()

	instanceName := "gornsh-go-allowed-" + filepath.Base(lConfigDir)

	// Go listener config
	prepareGornshConfigWithInstance(t, lConfigDir, instanceName, 0, 0)
	// Go initiator config
	prepareGornshConfigWithInstance(t, iConfigDir, instanceName, 0, 0)

	// Create identities
	initiatorID := mustCreateIdentity(t, iConfigDir, "initiator.id")
	allowedID := mustCreateIdentity(t, lConfigDir, "allowed.id")

	t.Logf("Initiator ID: %v", initiatorID.HexHash)
	t.Logf("Allowed ID:   %v", allowedID.HexHash)

	// Start listener allowed ONLY allowedID
	listener := startGornshListenerWithArgs(t, lConfigDir, "-a", allowedID.HexHash)
	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	// Wait for the shared instance transport to propagate.
	// Do NOT use waitForGornshConnection — it creates a real session on the listener
	// that interferes with subsequent test commands.
	time.Sleep(5 * time.Second)

	// Try to connect with initiatorID (which is NOT allowed)
	// The initiator should fail to establish a session.
	// We expect a non-zero exit code or at least not seeing "hello".
	// Based on gornsh implementation, it should exit with an error.
	output, exitCode := runGornshCommand(t, iConfigDir, 30*time.Second, "-i", filepath.Join(iConfigDir, "initiator.id"), "--timeout", "10", "-T", readyHash, "echo", "hello")

	// The listener should reject it. The initiator might see "link closed" or a protocol error.
	if exitCode == 0 && strings.Contains(output, "hello") {
		t.Fatalf("initiator (unauthorized) succeeded but should have been rejected\noutput:\n%v", output)
	}
	t.Logf("Initiator correctly failed with code %v and output: %q", exitCode, output)

	// PART 2: Success case
	// Try to connect with allowedID (which IS allowed)
	// We need to provide allowedID to initiator
	output, exitCode = runGornshCommand(t, iConfigDir, 30*time.Second, "-i", filepath.Join(lConfigDir, "allowed.id"), "--timeout", "10", "-T", readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("initiator (authorized) failed with code %v\noutput:\n%v\nlistener output:\n%v", exitCode, output, listener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("initiator (authorized) output missing hello\noutput:\n%v", output)
	}
	t.Logf("Initiator correctly succeeded with code 0 and output: %q", output)
}

func TestIntegrationMirrorFlag(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	configDir, cleanup := testutils.TempDir(t, "gornsh-mirror-")
	defer cleanup()

	instanceName := "gornsh-mirror-" + filepath.Base(configDir)
	prepareGornshConfigWithInstance(t, configDir, instanceName, 0, 0)

	listener := startGornshListener(t, configDir)
	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	// Wait for the shared instance transport to propagate.
	// Do NOT use waitForGornshConnection — it creates a real session on the listener
	// that interferes with subsequent test commands.
	time.Sleep(5 * time.Second)

	tests := []struct {
		name       string
		mirror     bool
		command    string
		wantExit   int
		wantStdout string
	}{
		{
			name:       "echo with mirror exits 0",
			mirror:     true,
			command:    "echo hello",
			wantExit:   0,
			wantStdout: "hello",
		},
		{
			name:       "echo without mirror exits 0",
			mirror:     false,
			command:    "echo hello",
			wantExit:   0,
			wantStdout: "hello",
		},
		{
			name:     "false with mirror exits 1",
			mirror:   true,
			command:  "false",
			wantExit: 1,
		},
		{
			name:     "false without mirror exits 0",
			mirror:   false,
			command:  "false",
			wantExit: 0,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--timeout", "10", "-T"}
			if tc.mirror {
				args = append(args, "--mirror")
			}
			args = append(args, readyHash)
			args = append(args, strings.Fields(tc.command)...)

			output, exitCode := runGornshCommand(t, configDir, 30*time.Second, args...)
			if exitCode != tc.wantExit {
				t.Errorf("exitCode = %v, want %v", exitCode, tc.wantExit)
			}
			if tc.wantStdout != "" && !strings.Contains(output, tc.wantStdout) {
				t.Errorf("output %q missing %q", output, tc.wantStdout)
			}
		})
	}
}

func TestIntegrationNoAuthOpenListener(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)

	t.Run("no-auth allows unknown identities", func(t *testing.T) {
		configDir, cleanup := testutils.TempDir(t, "gornsh-noauth-allow-")
		defer cleanup()

		instanceName := "gornsh-noauth-allow-" + filepath.Base(configDir)
		prepareGornshConfigWithInstance(t, configDir, instanceName, 0, 0)

		// Start listener with --no-auth (default in startGornshListener)
		listener := startGornshListener(t, configDir)
		readyHash := listener.hash()

		// Wait for the shared instance transport to propagate.
		// Do NOT use waitForGornshConnection — it creates a real session on the listener
		// that interferes with subsequent test commands.
		time.Sleep(5 * time.Second)

		// Initiator with a random identity should succeed
		output, exitCode := runGornshCommand(t, configDir, 30*time.Second, "--timeout", "10", "-T", readyHash, "echo", "hello")
		if exitCode != 0 || !strings.Contains(output, "hello") {
			t.Errorf("expected success with --no-auth, got exit %v, output: %q", exitCode, output)
		}
	})

	t.Run("default (auth enabled) denies unknown identities", func(t *testing.T) {
		configDir, cleanup := testutils.TempDir(t, "gornsh-noauth-deny-")
		defer cleanup()

		instanceName := "gornsh-noauth-deny-" + filepath.Base(configDir)
		prepareGornshConfigWithInstance(t, configDir, instanceName, 0, 0)

		// Start listener WITHOUT --no-auth
		listener := startGornshListenerWithArgs(t, configDir) // empty args = default behavior (auth enabled)
		readyHash := listener.hash()

		// Wait for the listener's path to propagate through the shared instance.
		// waitForGornshConnection cannot probe this listener since all identities
		// are denied (no allowlist configured). Use a fixed sleep instead.
		time.Sleep(5 * time.Second)

		// Initiator with any identity should fail
		output, exitCode := runGornshCommand(t, configDir, 30*time.Second, "--timeout", "10", "-T", readyHash, "echo", "hello")
		if exitCode == 0 && strings.Contains(output, "hello") {
			t.Errorf("expected failure without --no-auth and no allowlist, but succeeded")
		}

		// Verify warning in listener log
		// Python: "Authentication enabled but no allowed identities configured; denying all command requests"
		// Wait a bit for log to be written
		time.Sleep(2 * time.Second)
		logOut := listener.output()
		wantWarning := "denying all command requests"
		if !strings.Contains(logOut, wantWarning) {
			t.Errorf("listener log missing expected warning %q\nlog:\n%v", wantWarning, logOut)
		}
	})
}

func TestIntegrationNetworkPartitionRecovery(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	configDir, cleanup := testutils.TempDir(t, "gornsh-recovery-")
	defer cleanup()

	instanceName := "gornsh-recovery-" + filepath.Base(configDir)
	prepareGornshConfigWithInstance(t, configDir, instanceName, 0, 0)

	listener := startGornshListener(t, configDir)
	readyHash := listener.hash()

	// Wait for the shared instance transport to propagate.
	time.Sleep(5 * time.Second)

	// 1. Verify initial success
	output, exitCode := runGornshCommand(t, configDir, 30*time.Second, "--timeout", "10", "-T", readyHash, "echo", "step1")
	if exitCode != 0 || !strings.Contains(output, "step1") {
		t.Fatalf("Step 1 failed: exit %v, output: %q", exitCode, output)
	}

	// 2. Simulate partition (STOP listener)
	t.Log("Simulating network partition (SIGSTOP listener)")
	if err := listener.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("failed to stop listener: %v", err)
	}

	// Initiator should fail
	output, exitCode = runGornshCommand(t, configDir, 15*time.Second, "--timeout", "5", "-T", readyHash, "echo", "step2")
	if exitCode == 0 && strings.Contains(output, "step2") {
		t.Errorf("Step 2 should have failed but succeeded")
	}
	t.Logf("Step 2 correctly failed during partition")

	// 3. Restore network (CONT listener)
	t.Log("Restoring network (SIGCONT listener)")
	if err := listener.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("failed to continue listener: %v", err)
	}

	// Wait for the listener to recover from SIGSTOP
	time.Sleep(5 * time.Second)

	// Initiator should succeed again
	output, exitCode = runGornshCommand(t, configDir, 30*time.Second, "--timeout", "10", "-T", readyHash, "echo", "step3")
	if exitCode != 0 || !strings.Contains(output, "step3") {
		t.Fatalf("Step 3 failed after recovery: exit %v, output: %q\nlistener output:\n%v", exitCode, output, listener.output())
	}
	t.Log("Step 3 successfully recovered")
}

func mustCreateIdentity(t *testing.T, configDir string, filename string) *rns.Identity {
	t.Helper()
	id, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatalf("failed to create identity: %v", err)
	}
	path := filepath.Join(configDir, filename)
	if err := id.ToFile(path); err != nil {
		t.Fatalf("failed to save identity to %q: %v", path, err)
	}
	return id
}

func startGornshListenerWithArgs(t *testing.T, configDir string, extraArgs ...string) *gornshListenerProcess {
	t.Helper()

	args := append([]string{"--config", configDir, "-l", "-v"}, extraArgs...)
	cmd := exec.Command(getGornshBinaryPath(t), args...)
	cmd.Stdin = strings.NewReader("")
	cmd.Env = gornshIntegrationEnv("")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		t.Fatalf("failed to start gornsh listener: %v", err)
	}

	proc := &gornshListenerProcess{
		cmd:     cmd,
		stdout:  &bytes.Buffer{},
		readyCh: make(chan struct{}),
		waitCh:  make(chan error, 1),
	}

	go func() {
		defer func() {
			// Ensure we close the reader when done to prevent resource leaks
			_ = reader.Close()
		}()
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			proc.hashMu.Lock()
			proc.stdout.WriteString(line)
			proc.stdout.WriteByte('\n')
			if proc.value == "" {
				if hash := parseListenerHash(line); hash != "" {
					proc.value = hash
					close(proc.readyCh)
				}
			}
			proc.hashMu.Unlock()
		}
		if err := scanner.Err(); err != nil {
			proc.waitCh <- err
			return
		}
		proc.waitCh <- nil
	}()

	select {
	case <-proc.readyCh:
	case err := <-proc.waitCh:
		if err == nil {
			t.Fatal("listener exited before readiness line")
		}
		t.Fatalf("listener failed before readiness line: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for listener readiness; output so far:\n%v", proc.output())
	}

	t.Cleanup(func() {
		proc.stop(t)
	})

	_ = writer.Close()
	return proc
}

func startGornshListener(t *testing.T, configDir string) *gornshListenerProcess {
	return startGornshListenerWithArgs(t, configDir, "--no-auth")
}

var listenerHashRE = regexp.MustCompile(`rnsh listening for commands on <([0-9a-fA-F]+)>`)

func parseListenerHash(line string) string {
	matches := listenerHashRE.FindAllStringSubmatch(line, -1)
	if len(matches) > 0 && len(matches[0]) > 1 {
		return matches[0][1]
	}
	return ""
}

func (p *gornshListenerProcess) hash() string {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return p.value
}

func (p *gornshListenerProcess) output() string {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return p.stdout.String()
}

func (p *gornshListenerProcess) stop(t *testing.T) {
	t.Helper()
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("listener exit error: %v\n%v", err, p.output())
		}
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
}

func runGornshCommand(t *testing.T, configDir string, timeout time.Duration, args ...string) (string, int) {
	t.Helper()

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, getGornshBinaryPath(t), append([]string{"--config", configDir}, args...)...)
	cmd.Stdin = strings.NewReader("")
	cmd.Env = gornshIntegrationEnv("")

	t.Logf("Running gornsh command: %v", append([]string{"--config", configDir}, args...))
	out, err := cmd.CombinedOutput()
	t.Logf("Command finished. Output: %q, Error: %v", string(out), err)
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		t.Logf("Command exited with code: %d", exitErr.ExitCode())
		return string(out), exitErr.ExitCode()
	}
	// Check if it's a context deadline exceeded error
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("timeout running gornsh %v after %v: %v\n%v", args, timeout, err, string(out))
	}
	// Log the error for debugging
	t.Logf("failed to run gornsh %v: %v\n%v", args, err, string(out))
	return string(out), -1
}

func getGornshBinaryPath(t *testing.T) string {
	t.Helper()

	if gornshBinaryPath == "" {
		t.Fatal("gornsh binary path not initialized by TestMain")
	}
	return gornshBinaryPath
}

func gornshIntegrationEnv(pythonPathOverride string) []string {
	filtered := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "TERM", "LINES", "COLUMNS", "PYTHONPATH":
			continue
		default:
			filtered = append(filtered, entry)
		}
	}
	if len(filtered) == 0 {
		filtered = append(filtered, "HOME=/tmp")
	}
	if pythonPathOverride != "" {
		filtered = append(filtered, "PYTHONPATH="+pythonPathOverride)
	} else {
		pythonPath := getRnshPythonPath()
		if pythonPath != "" {
			filtered = append(filtered, "PYTHONPATH="+pythonPath)
		}
	}
	return filtered
}

func prepareGornshConfig(t *testing.T, configDir string) {
	prepareGornshConfigWithInstance(t, configDir, "gornsh-"+filepath.Base(configDir), 0, 0)
}

func prepareGornshConfigWithInstance(t *testing.T, configDir string, instanceName string, listenPort, forwardPort int) {
	t.Helper()

	if listenPort == 0 {
		configText := strings.Join([]string{
			"[reticulum]",
			"enable_transport = Yes",
			"share_instance = Yes",
			"instance_name = " + instanceName,
			"",
			"[logging]",
			"loglevel = 4",
			"",
			"[interfaces]",
			"  [[Default Interface]]",
			"    type = AutoInterface",
			"    enabled = Yes",
			"",
		}, "\n")
		if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
			t.Fatalf("failed to write gornsh config: %v", err)
		}
		return
	}

	configText := strings.Join([]string{
		"[reticulum]",
		"enable_transport = False",
		"share_instance = No",
		"instance_name = " + instanceName,
		"",
		"[logging]",
		"loglevel = 4",
		"",
		"[interfaces]",
		"  [[UDP Interface]]",
		"    type = UDPInterface",
		"    listen_ip = 127.0.0.1",
		"    listen_port = " + fmt.Sprintf("%v", listenPort),
		"    forward_ip = 127.0.0.1",
		"    forward_port = " + fmt.Sprintf("%v", forwardPort),
		"    enabled = Yes",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
		t.Fatalf("failed to write gornsh config: %v", err)
	}
}

func getRnshBinaryPath(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("rnsh")
	if err != nil {
		t.Skip("rnsh not found in PATH, skipping Python/Go integration tests")
	}
	return path
}

type pythonListenerProcess struct {
	cmd     *exec.Cmd
	stdout  *bytes.Buffer
	value   string
	hashMu  sync.Mutex
	readyCh chan struct{}
	waitCh  chan error
}

func startPythonListener(t *testing.T, configDir, instanceName string, listenPort, forwardPort int) *pythonListenerProcess {
	t.Helper()

	env := gornshIntegrationEnv("")

	cmd := exec.Command("python3", "-m", "rnsh.rnsh", "-l", "--no-auth", "-b", "0", "-c", configDir)
	cmd.Stdin = strings.NewReader("")
	cmd.Env = env
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error: %v", err)
	}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		t.Fatalf("failed to start Python listener: %v", err)
	}

	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	proc := &pythonListenerProcess{
		cmd:     cmd,
		stdout:  &bytes.Buffer{},
		readyCh: make(chan struct{}),
		waitCh:  make(chan error, 1),
	}

	go func() {
		defer func() {
			_ = reader.Close()
			_ = writer.Close()
		}()
		lineCount := 0
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			lineCount++
			log.Printf("[PY-SCANNER] line %v: %q", lineCount, line)
			proc.hashMu.Lock()
			proc.stdout.WriteString(line)
			proc.stdout.WriteByte('\n')
			if proc.value == "" {
				if hash := parseListenerHash(line); hash != "" {
					proc.value = hash
					log.Printf("[PY-SCANNER] hash found: %v, closing readyCh", hash)
					close(proc.readyCh)
				}
			}
			proc.hashMu.Unlock()
		}
		if err := scanner.Err(); err != nil {
			log.Printf("[PY-SCANNER] scanner error after %v lines: %v", lineCount, err)
			proc.waitCh <- err
			return
		}
		log.Printf("[PY-SCANNER] scanner finished after %v lines", lineCount)
		proc.waitCh <- nil
	}()

	select {
	case <-proc.readyCh:
	case err := <-proc.waitCh:
		if err == nil {
			t.Fatal("Python listener exited before readiness line")
		}
		t.Fatalf("Python listener failed before readiness line: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatalf("timed out waiting for Python listener readiness; output so far:\n%v", proc.output())
	}

	t.Cleanup(func() {
		proc.stop(t)
	})

	_ = writer.Close()
	return proc
}

func (p *pythonListenerProcess) hash() string {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return p.value
}

func (p *pythonListenerProcess) output() string {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return p.stdout.String()
}

func (p *pythonListenerProcess) stop(t *testing.T) {
	t.Helper()
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Python listener exit error: %v\n%v", err, p.output())
		}
	case <-time.After(2 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
}
