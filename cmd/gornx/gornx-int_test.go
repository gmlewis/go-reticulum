// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

var versionLineRE = regexp.MustCompile(`^[^[:space:]]+\s+[^[:space:]]+$`)

var gornxBinaryPath string
var gornstatusBinaryPath string
var gornpathBinaryPath string

func TestMain(m *testing.M) {
	// This entire suite will be skipped if `-short` is used.
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}

	binDir, cleanup := testutils.TempDirMain("gornx-bin-")

	gornxBinaryPath = filepath.Join(binDir, "gornx")
	build := exec.Command("go", "build", "-o", gornxBinaryPath, ".")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("failed to build gornx binary: %v\n", err)
	}

	gornstatusBinaryPath = filepath.Join(binDir, "gornstatus")
	buildStatus := exec.Command("go", "build", "-o", gornstatusBinaryPath, "../gornstatus")
	buildStatus.Stdout = os.Stdout
	buildStatus.Stderr = os.Stderr
	if err := buildStatus.Run(); err != nil {
		log.Fatalf("failed to build gornstatus binary: %v\n", err)
	}

	gornpathBinaryPath = filepath.Join(binDir, "gornpath")
	buildPath := exec.Command("go", "build", "-o", gornpathBinaryPath, "../gornpath")
	buildPath.Stdout = os.Stdout
	buildPath.Stderr = os.Stderr
	if err := buildPath.Run(); err != nil {
		log.Fatalf("failed to build gornpath binary: %v\n", err)
	}

	exitCode := m.Run()

	cleanup()
	out, err := exec.Command("/usr/bin/pkill", "-f", binDir).CombinedOutput()
	if err != nil {
		log.Printf("pkill -f %q failed: %v\n%s", binDir, err, out)
	}

	os.Exit(exitCode)
}

func getGornxBinaryPath(t *testing.T) string {
	t.Helper()
	if gornxBinaryPath == "" {
		t.Fatal("gornx binary path not initialized by TestMain")
	}
	return gornxBinaryPath
}

func getRnxPythonBinaryPath(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("rnx")
	if err != nil {
		t.Fatal("rnx not found in PATH, skipping Python/Go integration tests")
	}
	return path
}

func TestIntegrationVersionOutput(t *testing.T) {
	gornxBin := getGornxBinaryPath(t)
	out, err := exec.Command(gornxBin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornx --version failed: %v\n%v", err, string(out))
	}
	want := "gornx " + rns.VERSION + "\n"
	if got := string(out); got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestIntegrationNoArgs(t *testing.T) {
	gornxBin := getGornxBinaryPath(t)
	out, err := exec.Command(gornxBin).CombinedOutput()
	if err != nil {
		t.Fatalf("gornx with no args failed: %v\n%v", err, string(out))
	}
	got := string(out)
	if !strings.Contains(got, "usage: gornx") {
		t.Errorf("output missing usage line, got:\n%v", got)
	}
}

func TestIntegrationAllowedHashValidation(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	prepareGornxConfig(t, configDir)

	// 1. Invalid length
	out, err := exec.Command(gornxBin, "--config", configDir, "-l", "-a", "too_short").CombinedOutput()
	if err == nil {
		t.Errorf("gornx -a too_short should fail")
	}
	if !strings.Contains(string(out), "Allowed destination length is invalid") {
		t.Errorf("output missing length error, got:\n%v", string(out))
	}

	// 2. Invalid hex
	// Truncated hash length is 128 bits = 16 bytes = 32 hex chars
	invalidHex := strings.Repeat("g", 32)
	out, err = exec.Command(gornxBin, "--config", configDir, "-l", "-a", invalidHex).CombinedOutput()
	if err == nil {
		t.Errorf("gornx -a invalid_hex should fail")
	}
	if !strings.Contains(string(out), "Invalid destination entered") {
		t.Errorf("output missing hex error, got:\n%v", string(out))
	}
}

func TestIntegrationPrintIdentity(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	prepareGornxConfig(t, configDir)

	out, err := exec.Command(gornxBin, "--config", configDir, "-p").CombinedOutput()
	if err != nil {
		t.Fatalf("gornx -p failed: %v\n%v", err, string(out))
	}
	got := string(out)
	if !strings.Contains(got, "Identity     : ") {
		t.Errorf("output missing identity, got:\n%v", got)
	}
	if !strings.Contains(got, "Listening on : ") {
		t.Errorf("output missing listening info, got:\n%v", got)
	}
}

func TestIntegrationEmptyAllowListWarning(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	prepareGornxConfig(t, configDir)

	// Should show warning because auth is enabled but no identities configured
	cmd := exec.Command(gornxBin, "--config", configDir, "-l")
	buf := &safeBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	wantWarning := "Warning: No allowed identities configured, rncx will not accept any commands!"
	deadline := time.Now().Add(10 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), wantWarning) {
			found = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found {
		t.Errorf("output missing warning, got:\n%v", buf.String())
	}
}

func TestIntegrationEcho(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	lConfigDir, cleanup1 := testutils.TempDir(t, "gornx-echo-l-")
	defer cleanup1()
	iConfigDir, cleanup2 := testutils.TempDir(t, "gornx-echo-i-")
	defer cleanup2()

	lPort := testutils.ReserveUDPPort(t)
	iPort := testutils.ReserveUDPPort(t)

	prepareGornxConfigWithInstance(t, lConfigDir, "gornx-l", lPort, iPort)
	prepareGornxConfigWithInstance(t, iConfigDir, "gornx-i", iPort, lPort)

	// Start listener in background
	cmd := exec.Command(gornxBin, "--config", lConfigDir, "-l", "-n", "-v", "-v")
	lBuf := &safeBuffer{}
	cmd.Stdout = lBuf
	cmd.Stderr = lBuf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	// Wait for readiness
	var readyHash string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		line := firstLineWithPrefix(lBuf.String(), "rnx listening for commands on <")
		if line != "" {
			parts := strings.Split(line, "<")
			if len(parts) > 1 {
				readyHash = strings.Split(parts[1], ">")[0]
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if readyHash == "" {
		t.Fatalf("timed out waiting for listener readiness, output:\n%v", lBuf.String())
	}

	// Run initiator
	out, err := exec.Command(gornxBin, "--config", iConfigDir, "-v", "-w", "30", readyHash, "echo hello").CombinedOutput()
	if err != nil {
		t.Fatalf("initiator failed: %v\noutput:\n%v\nlistener output:\n%v", err, string(out), lBuf.String())
	}

	if !strings.Contains(string(out), "hello") {
		t.Fatalf("output missing hello, got:\n%v\nlistener output:\n%v", string(out), lBuf.String())
	}
}

func TestIntegrationDetailedOutput(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	lConfigDir, cleanup1 := testutils.TempDir(t, "gornx-detailed-l-")
	defer cleanup1()
	iConfigDir, cleanup2 := testutils.TempDir(t, "gornx-detailed-i-")
	defer cleanup2()

	lPort := testutils.ReserveUDPPort(t)
	iPort := testutils.ReserveUDPPort(t)

	prepareGornxConfigWithInstance(t, lConfigDir, "gornx-detailed-l", lPort, iPort)
	prepareGornxConfigWithInstance(t, iConfigDir, "gornx-detailed-i", iPort, lPort)

	// Start listener in background
	lCmd := exec.Command(gornxBin, "--config", lConfigDir, "-l", "-n")
	lBuf := &safeBuffer{}
	lCmd.Stdout = lBuf
	lCmd.Stderr = lBuf
	if err := lCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer lCmd.Process.Kill()

	// Wait for readiness
	var readyHash string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		line := firstLineWithPrefix(lBuf.String(), "rnx listening for commands on <")
		if line != "" {
			parts := strings.Split(line, "<")
			if len(parts) > 1 {
				readyHash = strings.Split(parts[1], ">")[0]
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if readyHash == "" {
		t.Fatalf("timed out waiting for listener readiness, output:\n%v", lBuf.String())
	}

	// Run initiator with -d
	out, err := exec.Command(gornxBin, "--config", iConfigDir, "-d", "-w", "30", readyHash, "echo hello").CombinedOutput()
	if err != nil {
		t.Fatalf("initiator failed: %v\noutput:\n%v\nlistener output:\n%v", err, string(out), lBuf.String())
	}

	got := string(out)
	if !strings.Contains(got, "--- End of remote output, rnx done ---") {
		t.Errorf("output missing summary header, got:\n%v", got)
	}
	if !strings.Contains(got, "Remote command execution took") {
		t.Errorf("output missing execution time, got:\n%v", got)
	}
	if !strings.Contains(got, "Remote wrote 6 bytes to stdout") {
		t.Errorf("output missing stdout length, got:\n%v", got)
	}
}

func TestIntegrationTruncatedOutputNotice(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	lConfigDir, cleanup1 := testutils.TempDir(t, "gornx-trunc-l-")
	defer cleanup1()
	iConfigDir, cleanup2 := testutils.TempDir(t, "gornx-trunc-i-")
	defer cleanup2()

	lPort := testutils.ReserveUDPPort(t)
	iPort := testutils.ReserveUDPPort(t)

	prepareGornxConfigWithInstance(t, lConfigDir, "gornx-trunc-l", lPort, iPort)
	prepareGornxConfigWithInstance(t, iConfigDir, "gornx-trunc-i", iPort, lPort)

	lCmd := exec.Command(gornxBin, "--config", lConfigDir, "-l", "-n")
	lBuf := &safeBuffer{}
	lCmd.Stdout = lBuf
	lCmd.Stderr = lBuf
	if err := lCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer lCmd.Process.Kill()

	var readyHash string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		line := firstLineWithPrefix(lBuf.String(), "rnx listening for commands on <")
		if line != "" {
			parts := strings.Split(line, "<")
			if len(parts) > 1 {
				readyHash = strings.Split(parts[1], ">")[0]
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if readyHash == "" {
		t.Fatalf("timed out waiting for listener readiness, output:\n%v", lBuf.String())
	}

	command := "sh -c 'printf 123456789; printf abcdef 1>&2'"
	out, err := exec.Command(gornxBin, "--config", iConfigDir, "--stdout", "5", "--stderr", "2", "-w", "30", readyHash, command).CombinedOutput()
	if err != nil {
		t.Fatalf("initiator failed: %v\noutput:\n%v\nlistener output:\n%v", err, string(out), lBuf.String())
	}

	got := string(out)
	for _, want := range []string{
		"12345",
		"ab",
		"Output truncated before being returned:",
		"  stdout truncated to 5 bytes",
		"  stderr truncated to 2 bytes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q, got:\n%v", want, got)
		}
	}
}

func TestIntegrationRemoteExecuteFalseExitCode(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	lConfigDir, cleanup1 := testutils.TempDir(t, "gornx-execfalse-l-")
	defer cleanup1()
	iConfigDir, cleanup2 := testutils.TempDir(t, "gornx-execfalse-i-")
	defer cleanup2()

	lPort := testutils.ReserveUDPPort(t)
	iPort := testutils.ReserveUDPPort(t)

	prepareGornxConfigWithInstance(t, lConfigDir, "gornx-execfalse-l", lPort, iPort)
	prepareGornxConfigWithInstance(t, iConfigDir, "gornx-execfalse-i", iPort, lPort)

	lCmd := exec.Command(gornxBin, "--config", lConfigDir, "-l", "-n")
	lBuf := &safeBuffer{}
	lCmd.Stdout = lBuf
	lCmd.Stderr = lBuf
	if err := lCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer lCmd.Process.Kill()

	var readyHash string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		line := firstLineWithPrefix(lBuf.String(), "rnx listening for commands on <")
		if line != "" {
			parts := strings.Split(line, "<")
			if len(parts) > 1 {
				readyHash = strings.Split(parts[1], ">")[0]
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if readyHash == "" {
		t.Fatalf("timed out waiting for listener readiness, output:\n%v", lBuf.String())
	}

	out, err := exec.Command(gornxBin, "--config", iConfigDir, "-w", "30", readyHash, "nonexistent_command_12345").CombinedOutput()
	if err == nil {
		t.Fatalf("initiator unexpectedly succeeded, output:\n%v", string(out))
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("initiator error type = %T, want *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 248 {
		t.Fatalf("exit code = %v, want 248\noutput:\n%v", exitErr.ExitCode(), string(out))
	}
	if !strings.Contains(string(out), "Remote could not execute command") {
		t.Fatalf("output missing remote execute failure message, got:\n%v", string(out))
	}
}

func TestIntegrationInteractiveLoop(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	lConfigDir, cleanup1 := testutils.TempDir(t, "gornx-inter-l-")
	defer cleanup1()
	iConfigDir, cleanup2 := testutils.TempDir(t, "gornx-inter-i-")
	defer cleanup2()

	lPort := testutils.ReserveUDPPort(t)
	iPort := testutils.ReserveUDPPort(t)

	prepareGornxConfigWithInstance(t, lConfigDir, "gornx-inter-l", lPort, iPort)
	prepareGornxConfigWithInstance(t, iConfigDir, "gornx-inter-i", iPort, lPort)

	// Start listener in background
	lCmd := exec.Command(gornxBin, "--config", lConfigDir, "-l", "-n")
	lBuf := &safeBuffer{}
	lCmd.Stdout = lBuf
	lCmd.Stderr = lBuf
	if err := lCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer lCmd.Process.Kill()

	// Wait for readiness
	var readyHash string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		line := firstLineWithPrefix(lBuf.String(), "rnx listening for commands on <")
		if line != "" {
			parts := strings.Split(line, "<")
			if len(parts) > 1 {
				readyHash = strings.Split(parts[1], ">")[0]
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if readyHash == "" {
		t.Fatalf("timed out waiting for listener readiness, output:\n%v", lBuf.String())
	}

	// Run initiator in interactive mode
	iCmd := exec.Command(gornxBin, "--config", iConfigDir, "-x", "-w", "30", readyHash)
	stdin, err := iCmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	iBuf := &safeBuffer{}
	iCmd.Stdout = iBuf
	iCmd.Stderr = iBuf

	if err := iCmd.Start(); err != nil {
		t.Fatal(err)
	}

	// 1. Wait for the Python-style prompt.
	prompt := "> "
	deadline = time.Now().Add(30 * time.Second)
	foundPrompt := false
	for time.Now().Before(deadline) {
		if strings.Contains(iBuf.String(), prompt) {
			foundPrompt = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !foundPrompt {
		t.Fatalf("timed out waiting for prompt, output:\n%v", iBuf.String())
	}
	if strings.HasSuffix(iBuf.String(), "<"+readyHash+"> ") {
		t.Fatalf("interactive prompt should not include destination hash, output:\n%v", iBuf.String())
	}

	// 2. Send first command.
	fmt.Fprintln(stdin, "echo inter1")

	// 3. Wait for first command output.
	deadline = time.Now().Add(10 * time.Second)
	foundOutput := false
	for time.Now().Before(deadline) {
		if strings.Contains(iBuf.String(), "inter1") {
			foundOutput = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !foundOutput {
		t.Fatalf("timed out waiting for command output, output:\n%v", iBuf.String())
	}

	// 4. Send a second command over the same interactive link.
	fmt.Fprintln(stdin, "echo inter2")
	deadline = time.Now().Add(10 * time.Second)
	foundSecondOutput := false
	for time.Now().Before(deadline) {
		if strings.Contains(iBuf.String(), "inter2") {
			foundSecondOutput = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !foundSecondOutput {
		t.Fatalf("timed out waiting for second command output, output:\n%v", iBuf.String())
	}

	// 5. Python accepts case-insensitive quit commands.
	fmt.Fprintln(stdin, "QUIT")

	if err := iCmd.Wait(); err != nil {
		t.Fatalf("interactive initiator exited with error: %v\noutput:\n%v", err, iBuf.String())
	}
}

func TestIntegrationInitiatorTimeout(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	configDir, cleanup := testutils.TempDir(t, "gornx-timeout-")
	defer cleanup()
	prepareGornxConfig(t, configDir)

	// Try to connect to a non-existent destination with a short timeout
	// It should fail with a timeout message.
	// Using a random hash that definitely doesn't exist.
	start := time.Now()
	out, err := exec.Command(gornxBin, "--config", configDir, "-w", "2", "01234567890123456789012345678901", "echo hi").CombinedOutput()
	duration := time.Since(start)

	if err == nil {
		t.Errorf("gornx should have failed but exited with 0")
	}
	got := string(out)
	if !strings.Contains(got, "Could not request path") && !strings.Contains(got, "Could not recall identity") && !strings.Contains(got, "Link establishment timed out") {
		t.Errorf("output missing timeout error, got:\n%v", got)
	}
	if duration < 2*time.Second {
		t.Errorf("test finished too quickly: %v", duration)
	}
}

func TestIntegrationResultDownloadTimeout(t *testing.T) {
	testutils.SkipShortIntegration(t)
	gornxBin := getGornxBinaryPath(t)
	lConfigDir, cleanup1 := testutils.TempDir(t, "gornx-resulttimeout-l-")
	defer cleanup1()
	iConfigDir, cleanup2 := testutils.TempDir(t, "gornx-resulttimeout-i-")
	defer cleanup2()

	lPort := testutils.ReserveUDPPort(t)
	iPort := testutils.ReserveUDPPort(t)

	prepareGornxConfigWithInstance(t, lConfigDir, "gornx-resulttimeout-l", lPort, iPort)
	prepareGornxConfigWithInstance(t, iConfigDir, "gornx-resulttimeout-i", iPort, lPort)

	lCmd := exec.Command(gornxBin, "--config", lConfigDir, "-l", "-n")
	lBuf := &safeBuffer{}
	lCmd.Stdout = lBuf
	lCmd.Stderr = lBuf
	if err := lCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer lCmd.Process.Kill()

	var readyHash string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		line := firstLineWithPrefix(lBuf.String(), "rnx listening for commands on <")
		if line != "" {
			parts := strings.Split(line, "<")
			if len(parts) > 1 {
				readyHash = strings.Split(parts[1], ">")[0]
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if readyHash == "" {
		t.Fatalf("timed out waiting for listener readiness, output:\n%v", lBuf.String())
	}

	command := "python3 -c 'import sys; sys.stdout.write(\"x\"*5000000)'"
	out, err := exec.Command(gornxBin, "--config", iConfigDir, "-w", "30", "-W", "0.05", readyHash, command).CombinedOutput()
	if err == nil {
		t.Fatalf("initiator unexpectedly succeeded, output:\n%v", string(out))
	}

	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("initiator error type = %T, want *exec.ExitError", err)
	}
	if exitErr.ExitCode() != 246 {
		t.Fatalf("exit code = %v, want 246\noutput:\n%v", exitErr.ExitCode(), string(out))
	}
	if !strings.Contains(string(out), "Receiving result failed") {
		t.Fatalf("output missing result timeout message, got:\n%v", string(out))
	}
}

func TestIntegrationDestOnly(t *testing.T) {
	gornxBin := getGornxBinaryPath(t)
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	prepareGornxConfig(t, configDir)

	out, err := exec.Command(gornxBin, "--config", configDir, "abcdef").CombinedOutput()
	if err != nil {
		t.Fatalf("gornx with only dest failed: %v\n%v", err, string(out))
	}
	got := string(out)
	if !strings.Contains(got, "usage: gornx") {
		t.Errorf("output missing usage line, got:\n%v", got)
	}
}

func firstLineWithPrefix(output, prefix string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, prefix) {
			return line
		}
	}
	return ""
}
