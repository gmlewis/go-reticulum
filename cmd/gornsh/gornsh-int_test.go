// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"bufio"
	"bytes"
	"flag"
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
	if got := getRnshPythonPath(); got == "" {
		t.Fatal("getRnshPythonPath() returned empty path")
	}
}

func TestIntegrationVersionOutputFormatParity(t *testing.T) {
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
	configDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	prepareGornshConfig(t, configDir)

	listener := startGornshListener(t, configDir)
	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	time.Sleep(time.Second)

	deadline := time.Now().Add(5 * time.Second)
	var output string
	var exitCode int
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		output, exitCode = runGornshCommand(t, configDir, "--timeout", "1", "-T", readyHash, "echo", "hello")
		if exitCode == 0 && strings.Contains(output, "hello") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	if exitCode != 0 {
		t.Fatalf("initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", exitCode, output, listener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("initiator output %q missing hello\nlistener output:\n%v", output, listener.output())
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

func startGornshListener(t *testing.T, configDir string) *gornshListenerProcess {
	t.Helper()

	cmd := exec.Command(getGornshBinaryPath(t), "--config", configDir, "-l", "--no-auth", "-v")
	cmd.Stdin = strings.NewReader("")
	cmd.Env = gornshIntegrationEnv()
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

func runGornshCommand(t *testing.T, configDir string, args ...string) (string, int) {
	t.Helper()

	cmd := exec.Command(getGornshBinaryPath(t), append([]string{"--config", configDir}, args...)...)
	cmd.Stdin = strings.NewReader("")
	cmd.Env = gornshIntegrationEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(out), exitErr.ExitCode()
	}
	t.Fatalf("failed to run gornsh %v: %v\n%v", args, err, string(out))
	return "", 0
}

func getGornshBinaryPath(t *testing.T) string {
	t.Helper()

	if gornshBinaryPath == "" {
		t.Fatal("gornsh binary path not initialized by TestMain")
	}
	return gornshBinaryPath
}

func gornshIntegrationEnv() []string {
	filtered := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "TERM", "LINES", "COLUMNS":
			continue
		default:
			filtered = append(filtered, entry)
		}
	}
	if len(filtered) == 0 {
		filtered = append(filtered, "HOME=/tmp")
	}
	return filtered
}

func prepareGornshConfig(t *testing.T, configDir string) {
	prepareGornshConfigWithInstance(t, configDir, "gornsh-"+filepath.Base(configDir))
}

func prepareGornshConfigWithInstance(t *testing.T, configDir string, instanceName string) {
	t.Helper()

	configText := strings.Join([]string{
		"[reticulum]",
		"enable_transport = False",
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
}

func getRnshBinaryPath(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("rnsh")
	if err != nil {
		t.Skip("rnsh not found in PATH, skipping Python/Go integration tests")
	}
	return path
}
