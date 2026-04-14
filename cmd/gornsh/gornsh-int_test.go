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
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
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

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

const pythonListenerWrapper = `
import sys
import os

# Force unbuffered output
if sys.version_info >= (3, 7):
    import io
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, line_buffering=True)

# Add original repo to path
repo_dir = os.environ.get("ORIGINAL_RNSH_REPO_DIR")
if repo_dir:
    sys.path.insert(0, repo_dir)

try:
    import rnsh.rnsh as rnsh
except ImportError as e:
    print(f"FATAL: Could not import required modules: {e}", file=sys.stderr)
    sys.exit(1)

if __name__ == "__main__":
    try:
        rnsh.rnsh_cli()
    except KeyboardInterrupt:
        pass
    except Exception as e:
        print(f"FATAL: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        sys.exit(1)
`

var versionLineRE = regexp.MustCompile(`^[^[:space:]]+\s+[^[:space:]]+$`)

const (
	listenerReadinessTimeout  = 15 * time.Second
	sharedInstancePathTimeout = 10 * time.Second
)

var gornshBinaryPath string
var gornstatusBinaryPath string
var gornpathBinaryPath string

func TestMain(m *testing.M) {
	// This entire suite will be skipped if `-short` is used.
	flag.Parse()
	if testing.Short() {
		os.Exit(0)
	}

	binDir, cleanup := testutils.TempDirMain("gornsh-bin-")

	gornshBinaryPath = filepath.Join(binDir, "gornsh")
	build := exec.Command("go", "build", "-o", gornshBinaryPath, ".")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		log.Fatalf("failed to build gornsh binary: %v\n", err)
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

	// This section used to be in a `defer func() {...}()` but the `os.Exit` was
	// preventing it from being run, so this section ensures that it actually runs
	// before the process exits.
	cleanup()
	out, err := exec.Command("/usr/bin/pkill", "-f", binDir).CombinedOutput()
	if err != nil && err.Error() != "exit status 1" {
		log.Printf("pkill -f %q failed: %v\n%s", binDir, err, out)
	}

	os.Exit(exitCode)
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
	waitForPathInSharedInstance(t, configDir, readyHash, sharedInstancePathTimeout)

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

func TestIntegrationPythonListenerGoInitiatorEcho(t *testing.T) {
	testutils.SkipShortIntegration(t)
	listenerConfigDir, initiatorConfigDir, cleanup := prepareGornshDirectUDPConfigPair(t, "gornsh-py-go-")
	defer cleanup()

	pythonListener := startPythonListener(t, listenerConfigDir, "-b", "1")
	readyHash := pythonListener.hash()
	if readyHash == "" {
		t.Fatal("Python listener hash is empty")
	}

	output, exitCode := runGornshCommand(t, initiatorConfigDir, 15*time.Second, "--timeout", "8", "-T", readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", exitCode, output, pythonListener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("initiator output %q missing hello\nlistener output:\n%v", output, pythonListener.output())
	}
}

func TestIntegrationGoListenerPythonInitiatorEcho(t *testing.T) {
	testutils.SkipShortIntegration(t)
	listenerConfigDir, initiatorConfigDir, cleanup := prepareGornshDirectUDPConfigPair(t, "gornsh-go-py-")
	defer cleanup()

	listener := startGornshListenerWithArgs(t, listenerConfigDir, "--no-auth", "--announce", "1")
	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	output, exitCode := runRnshCommand(t, initiatorConfigDir, 15*time.Second, "--timeout", "8", "-T", readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("Python initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", exitCode, output, listener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("Python initiator output %q missing hello\nlistener output:\n%v", output, listener.output())
	}
}

func TestIntegrationPythonListenerGoInitiatorEchoWithoutGornpathPolling(t *testing.T) {
	testutils.SkipShortIntegration(t)
	oldPath := gornpathBinaryPath
	gornpathBinaryPath = filepath.Join(os.TempDir(), "missing-gornpath-binary")
	t.Cleanup(func() {
		gornpathBinaryPath = oldPath
	})

	listenerConfigDir, initiatorConfigDir, cleanup := prepareGornshDirectUDPConfigPair(t, "gornsh-py-go-nopath-")
	defer cleanup()

	pythonListener := startPythonListener(t, listenerConfigDir, "-b", "1")
	readyHash := pythonListener.hash()
	if readyHash == "" {
		t.Fatal("Python listener hash is empty")
	}

	output, exitCode := runGornshCommand(t, initiatorConfigDir, 15*time.Second, "--timeout", "8", "-T", readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", exitCode, output, pythonListener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("initiator output %q missing hello\nlistener output:\n%v", output, pythonListener.output())
	}
}

func TestIntegrationPythonListenerGoInitiatorEchoRepeatedHandshakes(t *testing.T) {
	testutils.SkipShortIntegration(t)

	for iteration := 0; iteration < 3; iteration++ {
		iteration := iteration
		t.Run(fmt.Sprintf("iteration-%d", iteration), func(t *testing.T) {
			listenerConfigDir, initiatorConfigDir, cleanup := prepareGornshDirectUDPConfigPair(t, fmt.Sprintf("gornsh-py-go-repeat-%d-", iteration))
			defer cleanup()

			pythonListener := startPythonListener(t, listenerConfigDir, "-b", "1")
			readyHash := pythonListener.hash()
			if readyHash == "" {
				t.Fatalf("iteration %d: Python listener hash is empty", iteration)
			}

			output, exitCode := runGornshCommand(t, initiatorConfigDir, 15*time.Second, "--timeout", "8", "-T", readyHash, "echo", fmt.Sprintf("hello-%d", iteration))
			if exitCode != 0 {
				t.Fatalf("iteration %d: initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", iteration, exitCode, output, pythonListener.output())
			}
			if !strings.Contains(output, fmt.Sprintf("hello-%d", iteration)) {
				t.Fatalf("iteration %d: initiator output %q missing hello\nlistener output:\n%v", iteration, output, pythonListener.output())
			}
		})
	}
}

func TestIntegrationGoListenerPythonInitiatorEchoWithoutGornpathPolling(t *testing.T) {
	testutils.SkipShortIntegration(t)
	oldPath := gornpathBinaryPath
	gornpathBinaryPath = filepath.Join(os.TempDir(), "missing-gornpath-binary")
	t.Cleanup(func() {
		gornpathBinaryPath = oldPath
	})

	listenerConfigDir, initiatorConfigDir, cleanup := prepareGornshDirectUDPConfigPair(t, "gornsh-go-py-nopath-")
	defer cleanup()

	listener := startGornshListenerWithArgs(t, listenerConfigDir, "--no-auth", "--announce", "1")
	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	output, exitCode := runRnshCommand(t, initiatorConfigDir, 15*time.Second, "--timeout", "8", "-T", readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("Python initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", exitCode, output, listener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("Python initiator output %q missing hello\nlistener output:\n%v", output, listener.output())
	}
}

func TestIntegrationGoListenerPythonInitiatorEchoRepeatedHandshakes(t *testing.T) {
	testutils.SkipShortIntegration(t)

	for iteration := 0; iteration < 3; iteration++ {
		iteration := iteration
		t.Run(fmt.Sprintf("iteration-%d", iteration), func(t *testing.T) {
			listenerConfigDir, initiatorConfigDir, cleanup := prepareGornshDirectUDPConfigPair(t, fmt.Sprintf("gornsh-go-py-repeat-%d-", iteration))
			defer cleanup()

			listener := startGornshListenerWithArgs(t, listenerConfigDir, "--no-auth", "--announce", "1")
			readyHash := listener.hash()
			if readyHash == "" {
				t.Fatalf("iteration %d: listener hash is empty", iteration)
			}

			output, exitCode := runRnshCommand(t, initiatorConfigDir, 15*time.Second, "--timeout", "8", "-T", readyHash, "echo", fmt.Sprintf("hello-%d", iteration))
			if exitCode != 0 {
				t.Fatalf("iteration %d: Python initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", iteration, exitCode, output, listener.output())
			}
			if !strings.Contains(output, fmt.Sprintf("hello-%d", iteration)) {
				t.Fatalf("iteration %d: Python initiator output %q missing hello\nlistener output:\n%v", iteration, output, listener.output())
			}
		})
	}
}

func TestIntegrationReadyListenerServesUnderModerateLocalLoad(t *testing.T) {
	testutils.SkipShortIntegration(t)

	listenerConfigDir, initiatorConfigDir, cleanup := prepareGornshDirectUDPConfigPair(t, "gornsh-load-")
	defer cleanup()

	listener := startGornshListenerWithArgs(t, listenerConfigDir, "--no-auth", "--announce", "1")
	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	workerCount := runtime.GOMAXPROCS(0) / 2
	if workerCount < 2 {
		workerCount = 2
	}
	stopLoad := make(chan struct{})
	var loadWG sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		loadWG.Add(1)
		go func(seed byte) {
			defer loadWG.Done()
			payload := bytes.Repeat([]byte{seed}, 2048)
			for {
				select {
				case <-stopLoad:
					return
				default:
					_ = sha256.Sum256(payload)
				}
			}
		}(byte(worker + 1))
	}
	defer func() {
		close(stopLoad)
		loadWG.Wait()
	}()

	started := time.Now()
	output, exitCode := runGornshCommand(t, initiatorConfigDir, 10*time.Second, "--timeout", "5", "-T", readyHash, "echo", "loaded")
	elapsed := time.Since(started)
	if exitCode != 0 {
		t.Fatalf("initiator exit code = %v, want 0\ninitiator output:\n%v\nlistener output:\n%v", exitCode, output, listener.output())
	}
	if !strings.Contains(output, "loaded") {
		t.Fatalf("initiator output %q missing loaded\nlistener output:\n%v", output, listener.output())
	}
	if elapsed > 10*time.Second {
		t.Fatalf("loaded request completed in %v, want <= 10s", elapsed)
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

func prepareGornshDirectUDPConfigPair(t *testing.T, prefix string) (string, string, func()) {
	t.Helper()

	listenerConfigDir, cleanupListener := testutils.TempDir(t, prefix+"listener-")
	initiatorConfigDir, cleanupInitiator := testutils.TempDir(t, prefix+"initiator-")
	cleanup := func() {
		cleanupInitiator()
		cleanupListener()
	}

	listenerPort := testutils.ReserveUDPPort(t)
	initiatorPort := testutils.ReserveUDPPort(t)
	prepareGornshDirectUDPConfig(t, listenerConfigDir, "gornsh-listener-"+filepath.Base(listenerConfigDir), listenerPort, initiatorPort)
	prepareGornshDirectUDPConfig(t, initiatorConfigDir, "gornsh-initiator-"+filepath.Base(initiatorConfigDir), initiatorPort, listenerPort)

	return listenerConfigDir, initiatorConfigDir, cleanup
}

func prepareGornshDirectUDPConfig(t *testing.T, configDir, instanceName string, listenPort, forwardPort int) {
	t.Helper()

	configText := strings.Join([]string{
		"[reticulum]",
		"enable_transport = Yes",
		"share_instance = No",
		"instance_name = " + instanceName,
		"",
		"[logging]",
		"loglevel = 4",
		"",
		"[interfaces]",
		"  [[Default Interface]]",
		"    type = UDPInterface",
		"    enabled = Yes",
		"    listen_ip = 127.0.0.1",
		"    listen_port = " + fmt.Sprintf("%v", listenPort),
		"    forward_ip = 127.0.0.1",
		"    forward_port = " + fmt.Sprintf("%v", forwardPort),
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(configText), 0o600); err != nil {
		t.Fatalf("failed to write gornsh direct UDP config: %v", err)
	}
}

type gornshListenerProcess struct {
	cmd          *exec.Cmd
	stdout       *bytes.Buffer
	value        string
	bootstrapped bool
	hashMu       sync.Mutex
	readyCh      chan struct{}
	waitCh       chan error
}

func TestWaitForListenerReadinessTimesOutAfterBootstrapLine(t *testing.T) {
	t.Parallel()

	proc := &pythonListenerProcess{
		stdout:  &bytes.Buffer{},
		readyCh: make(chan struct{}),
		waitCh:  make(chan error, 1),
	}
	proc.recordLine(listeningReadyLine())

	start := time.Now()
	err := waitForListenerReadiness("Python listener", proc.readyCh, proc.waitCh, proc.output, proc.bootstrappedReadyLineSeen, 50*time.Millisecond)
	if err == nil {
		t.Fatal("waitForListenerReadiness() error = nil, want bootstrap stall error")
	}
	if !strings.Contains(err.Error(), "stalled after bootstrap line before destination hash") {
		t.Fatalf("waitForListenerReadiness() error = %q, want bootstrap stall detail", err)
	}
	if !strings.Contains(err.Error(), listeningReadyLine()) {
		t.Fatalf("waitForListenerReadiness() error missing captured bootstrap output: %q", err)
	}
	if strings.Contains(err.Error(), "Execute command message") {
		t.Fatalf("waitForListenerReadiness() error = %q, want failure before any command execution begins", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("waitForListenerReadiness() took %v, want under 500ms", elapsed)
	}
}

func TestWaitForListenerReadinessReportsExitBeforeReadinessLine(t *testing.T) {
	t.Parallel()

	proc := &pythonListenerProcess{
		stdout:  &bytes.Buffer{},
		readyCh: make(chan struct{}),
		waitCh:  make(chan error, 1),
	}
	proc.waitCh <- nil

	err := waitForListenerReadiness("Python listener", proc.readyCh, proc.waitCh, proc.output, proc.bootstrappedReadyLineSeen, 50*time.Millisecond)
	if err == nil {
		t.Fatal("waitForListenerReadiness() error = nil, want early-exit error")
	}
	if !strings.Contains(err.Error(), "exited before readiness line") {
		t.Fatalf("waitForListenerReadiness() error = %q, want early-exit detail", err)
	}
}

func TestWaitForListenerReadinessIgnoresPostReadyFailure(t *testing.T) {
	t.Parallel()

	proc := &pythonListenerProcess{
		stdout:  &bytes.Buffer{},
		readyCh: make(chan struct{}),
		waitCh:  make(chan error, 1),
	}
	proc.recordLine(listeningReadyLine())
	if !proc.recordLine("rnsh listening for commands on <deadbeef>") {
		t.Fatal("recordLine() = false, want readiness hash detection")
	}
	close(proc.readyCh)
	go func() {
		time.Sleep(10 * time.Millisecond)
		proc.waitCh <- fmt.Errorf("later link failure")
	}()

	if err := waitForListenerReadiness("Python listener", proc.readyCh, proc.waitCh, proc.output, proc.bootstrappedReadyLineSeen, 50*time.Millisecond); err != nil {
		t.Fatalf("waitForListenerReadiness() error = %v, want nil after readiness", err)
	}
}

func TestListenerReadinessTimeoutBounded(t *testing.T) {
	t.Parallel()

	if listenerReadinessTimeout >= 120*time.Second {
		t.Fatalf("listenerReadinessTimeout = %v, want far below legacy 120s waits", listenerReadinessTimeout)
	}
	if listenerReadinessTimeout > 15*time.Second {
		t.Fatalf("listenerReadinessTimeout = %v, want low-double-digit bound", listenerReadinessTimeout)
	}
}

func TestSharedInstancePathTimeoutBounded(t *testing.T) {
	t.Parallel()

	if sharedInstancePathTimeout >= 120*time.Second {
		t.Fatalf("sharedInstancePathTimeout = %v, want far below legacy 120s waits", sharedInstancePathTimeout)
	}
	if sharedInstancePathTimeout > 10*time.Second {
		t.Fatalf("sharedInstancePathTimeout = %v, want helper polling window no greater than 10s", sharedInstancePathTimeout)
	}
}

func TestIntegrationAllowedIdentityEnforcement(t *testing.T) {
	testutils.SkipShortIntegration(t)
	configDir, cleanup := testutils.TempDir(t, "gornsh-go-allowed-")
	defer cleanup()

	instanceName := "gornsh-go-allowed-" + filepath.Base(configDir)
	prepareGornshConfigWithInstance(t, configDir, instanceName, 0, 0)

	// Create identities
	initiatorID := mustCreateIdentity(t, configDir, "initiator.id")
	allowedID := mustCreateIdentity(t, configDir, "allowed.id")

	t.Logf("Initiator ID: %v", initiatorID.HexHash)
	t.Logf("Allowed ID:   %v", allowedID.HexHash)

	// Start listener allowed ONLY allowedID
	listener := startGornshListenerWithArgs(t, configDir, "-a", allowedID.HexHash)
	readyHash := listener.hash()
	if readyHash == "" {
		t.Fatal("listener hash is empty")
	}

	// Wait for the shared instance transport to propagate.
	waitForPathInSharedInstance(t, configDir, readyHash, sharedInstancePathTimeout)

	// Try to connect with initiatorID (which is NOT allowed)
	output, exitCode := runGornshCommand(t, configDir, 30*time.Second, "-i", filepath.Join(configDir, "initiator.id"), "--timeout", "10", "-T", readyHash, "echo", "hello")

	if exitCode == 0 && strings.Contains(output, "hello") {
		t.Fatalf("initiator (unauthorized) succeeded but should have been rejected\noutput:\n%v", output)
	}
	t.Logf("Initiator correctly failed with code %v and output: %q", exitCode, output)

	// PART 2: Success case
	output, exitCode = runGornshCommand(t, configDir, 30*time.Second, "-i", filepath.Join(configDir, "allowed.id"), "--timeout", "10", "-T", readyHash, "echo", "hello")
	if exitCode != 0 {
		t.Fatalf("initiator (authorized) failed with code %v\noutput:\n%v\nlistener output:\n%v", exitCode, output, listener.output())
	}
	if !strings.Contains(output, "hello") {
		t.Fatalf("initiator (authorized) output missing hello\noutput:\n%v", output)
	}
	t.Logf("Initiator correctly succeeded with code 0 and output: %q", output)
}

func TestIntegrationMirrorFlag(t *testing.T) {
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
	waitForPathInSharedInstance(t, configDir, readyHash, sharedInstancePathTimeout)

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
		waitForPathInSharedInstance(t, configDir, readyHash, sharedInstancePathTimeout)

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
		waitForPathInSharedInstance(t, configDir, readyHash, sharedInstancePathTimeout)

		// Initiator with any identity should fail
		output, exitCode := runGornshCommand(t, configDir, 30*time.Second, "--timeout", "10", "-T", readyHash, "echo", "hello")
		if exitCode == 0 && strings.Contains(output, "hello") {
			t.Errorf("expected failure without --no-auth and no allowlist, but succeeded")
		}

		// Verify warning in listener log
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
	testutils.SkipShortIntegration(t)
	configDir, cleanup := testutils.TempDir(t, "gornsh-recovery-")
	defer cleanup()

	instanceName := "gornsh-recovery-" + filepath.Base(configDir)
	prepareGornshConfigWithInstance(t, configDir, instanceName, 0, 0)

	listener := startGornshListener(t, configDir)
	readyHash := listener.hash()

	// Wait for the shared instance transport to propagate.
	waitForPathInSharedInstance(t, configDir, readyHash, sharedInstancePathTimeout)

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
	waitForPathInSharedInstance(t, configDir, readyHash, sharedInstancePathTimeout)

	// Initiator should succeed again
	output, exitCode = runGornshCommand(t, configDir, 30*time.Second, "--timeout", "10", "-T", readyHash, "echo", "step3")
	if exitCode != 0 || !strings.Contains(output, "step3") {
		t.Fatalf("Step 3 failed after recovery: exit %v, output: %q\nlistener output:\n%v", exitCode, output, listener.output())
	}
	logOut := strings.ToLower(listener.output())
	if !strings.Contains(logOut, "broken pipe") {
		t.Fatalf("listener recovery log missing expected broken-pipe symptom\nlistener output:\n%v", listener.output())
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
		lineCount := 0
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()
			lineCount++
			log.Printf("[GO-SCANNER] line %v: %q", lineCount, line)
			if proc.recordLine(line) {
				log.Printf("[GO-SCANNER] hash found: %v, closing readyCh", proc.hash())
				select {
				case <-proc.readyCh:
				default:
					close(proc.readyCh)
				}
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("[GO-SCANNER] scanner error after %v lines: %v", lineCount, err)
			proc.waitCh <- err
			return
		}
		log.Printf("[GO-SCANNER] scanner finished after %v lines", lineCount)
		proc.waitCh <- nil
	}()

	if err := waitForListenerReadiness("gornsh listener", proc.readyCh, proc.waitCh, proc.output, proc.bootstrappedReadyLineSeen, listenerReadinessTimeout); err != nil {
		t.Fatal(err)
	}
	log.Printf("gornsh listener hash is ready: %v", proc.hash())

	t.Cleanup(func() {
		proc.stop(t)
	})

	_ = writer.Close()
	return proc
}

func startGornshListener(t *testing.T, configDir string) *gornshListenerProcess {
	return startGornshListenerWithArgs(t, configDir, "--no-auth")
}

var listenerHashRE = regexp.MustCompile(`(?i)rnsh listening for commands on <([0-9a-fA-F]+)>`)

func parseListenerHash(line string) string {
	matches := listenerHashRE.FindAllStringSubmatch(line, -1)
	if len(matches) > 0 && len(matches[0]) > 1 {
		return matches[0][1]
	}
	// log.Printf("[DEBUG] parseListenerHash: no match for %q", line)
	return ""
}

func (p *gornshListenerProcess) hash() string {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return p.value
}

func (p *gornshListenerProcess) bootstrappedReadyLineSeen() bool {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return p.bootstrapped
}

func (p *gornshListenerProcess) output() string {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return p.stdout.String()
}

func (p *gornshListenerProcess) recordLine(line string) bool {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return recordListenerLine(p.stdout, &p.value, &p.bootstrapped, line)
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

func waitForPathInSharedInstance(t *testing.T, configDir, hash string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	// We use gornpath to poll the shared instance.
	// It will exit with 0 if it finds a path.
	for time.Now().Before(deadline) {
		cmd := exec.Command(gornpathBinaryPath, "--config", configDir, "-w", "1", hash)
		cmd.Env = gornshIntegrationEnv("")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Logf("Found path to %v: %s", hash, string(out))
			return
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("timed out waiting for path to %v in shared instance", hash)
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
	cmd          *exec.Cmd
	stdout       *bytes.Buffer
	value        string
	bootstrapped bool
	hashMu       sync.Mutex
	readyCh      chan struct{}
	waitCh       chan error
}

func startPythonListener(t *testing.T, configDir string, extraArgs ...string) *pythonListenerProcess {
	t.Helper()

	wrapperPath := filepath.Join(os.TempDir(), "gornsh_py_wrapper_"+filepath.Base(configDir)+".py")
	if err := os.WriteFile(wrapperPath, []byte(pythonListenerWrapper), 0o755); err != nil {
		t.Fatalf("failed to write wrapper script: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(wrapperPath) })

	env := gornshIntegrationEnv("")

	args := append([]string{wrapperPath, "-l", "--no-auth"}, extraArgs...)
	args = append(args, "-c", configDir)
	cmd := exec.Command("python3", args...)
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
		// Increase buffer for large logs
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			lineCount++
			log.Printf("[PY-SCANNER] line %v: %q", lineCount, line)
			if proc.recordLine(line) {
				log.Printf("[PY-SCANNER] hash found: %v, closing readyCh", proc.hash())
				select {
				case <-proc.readyCh:
				default:
					close(proc.readyCh)
				}
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("[PY-SCANNER] scanner error after %v lines: %v", lineCount, err)
			proc.waitCh <- err
			return
		}
		log.Printf("[PY-SCANNER] scanner finished after %v lines", lineCount)
		proc.waitCh <- nil
	}()

	if err := waitForListenerReadiness("Python listener", proc.readyCh, proc.waitCh, proc.output, proc.bootstrappedReadyLineSeen, listenerReadinessTimeout); err != nil {
		t.Fatal(err)
	}
	log.Printf("gornsh listener hash is ready: %v", proc.hash())

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

func (p *pythonListenerProcess) bootstrappedReadyLineSeen() bool {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return p.bootstrapped
}

func (p *pythonListenerProcess) output() string {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return p.stdout.String()
}

func (p *pythonListenerProcess) recordLine(line string) bool {
	p.hashMu.Lock()
	defer p.hashMu.Unlock()
	return recordListenerLine(p.stdout, &p.value, &p.bootstrapped, line)
}

func recordListenerLine(stdout *bytes.Buffer, value *string, bootstrapped *bool, line string) bool {
	stdout.WriteString(line)
	stdout.WriteByte('\n')
	if !*bootstrapped && strings.Contains(line, listeningReadyLine()) {
		*bootstrapped = true
	}
	if *value == "" {
		if hash := parseListenerHash(line); hash != "" {
			*value = hash
			return true
		}
	}
	return false
}

func waitForListenerReadiness(name string, readyCh <-chan struct{}, waitCh <-chan error, outputFn func() string, bootstrappedFn func() bool, timeout time.Duration) error {
	select {
	case <-readyCh:
		return nil
	case err := <-waitCh:
		if err == nil {
			if bootstrappedFn() {
				return fmt.Errorf("%v exited after bootstrap line before destination hash; output so far:\n%v", name, outputFn())
			}
			return fmt.Errorf("%v exited before readiness line; output so far:\n%v", name, outputFn())
		}
		if bootstrappedFn() {
			return fmt.Errorf("%v failed after bootstrap line before destination hash: %v\noutput so far:\n%v", name, err, outputFn())
		}
		return fmt.Errorf("%v failed before readiness line: %v\noutput so far:\n%v", name, err, outputFn())
	case <-time.After(timeout):
		if bootstrappedFn() {
			return fmt.Errorf("%v stalled after bootstrap line before destination hash within %v; output so far:\n%v", name, timeout, outputFn())
		}
		return fmt.Errorf("%v timed out waiting for readiness line within %v; output so far:\n%v", name, timeout, outputFn())
	}
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
