// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var versionLineRE = regexp.MustCompile(`^[^[:space:]]+\s+[^[:space:]]+$`)

func TestIntegrationScaffoldHelpers(t *testing.T) {
	t.Parallel()

	if got := tempDir(t); got == "" {
		t.Fatal("tempDir() returned empty path")
	}
	if got := getRnshPythonPath(); got == "" {
		t.Fatal("getRnshPythonPath() returned empty path")
	}
}

func TestIntegrationVersionOutputFormatParity(t *testing.T) {
	t.Parallel()

	pythonBin := getRnshBinaryPath(t)
	gornshBin := buildGornsh(t)

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
	gornshBin := buildGornsh(t)
	configDir := tempDir(t)

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
	gornshBin := buildGornsh(t)
	configDir := tempDir(t)

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

	configDir := tempDir(t)
	gornshBin := buildGornsh(t)
	listener := startGornshListener(t, gornshBin, configDir)

	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	output, exitCode := runGornshCommand(t, gornshBin, "--config", configDir, readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("initiator exit code = %v, want 0\n%v", exitCode, output)
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("initiator output %q missing hello", output)
	}
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

func startGornshListener(t *testing.T, bin string, configDir string) *gornshListenerProcess {
	t.Helper()

	cmd := exec.Command(bin, "--config", configDir, "-l", "--no-auth")
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
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for listener readiness; output so far:\n%v", proc.output())
	}

	t.Cleanup(func() {
		proc.stop(t)
	})

	_ = writer.Close()
	return proc
}

func parseListenerHash(line string) string {
	const prefix = "rnsh listening for commands on <"
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, ">") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(line, prefix), ">")
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
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("listener exit error: %v\n%v", err, p.output())
	}
}

func runGornshCommand(t *testing.T, bin string, args ...string) (string, int) {
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

func getRnshBinaryPath(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("rnsh")
	if err != nil {
		t.Skip("rnsh not found in PATH, skipping Python/Go integration tests")
	}
	return path
}

func tempDir(t *testing.T) string {
	t.Helper()

	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "gornsh-int-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp() error: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func buildGornsh(t *testing.T) string {
	t.Helper()

	tmpDir := tempDir(t)
	bin := filepath.Join(tmpDir, "gornsh")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build gornsh: %v\n%v", err, string(out))
	}
	return bin
}
