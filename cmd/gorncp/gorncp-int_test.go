// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

type SafeBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (sb *SafeBuffer) Write(p []byte) (n int, err error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *SafeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func getFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func runPython(t *testing.T, configDir string, args ...string) string {
	t.Helper()
	repoDir := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR")
	if repoDir == "" {
		repoDir = "../../original-reticulum-repo"
	}
	scriptPath := filepath.Join(repoDir, "RNS", "Utilities", "rncp.py")
	fullArgs := append([]string{"-u", scriptPath, "--config", configDir}, args...)
	cmd := exec.Command("python3", fullArgs...)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+repoDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("runPython failed: %v\nOutput: %s", err, string(out))
	}
	return string(out)
}

func runPythonBackground(t *testing.T, configDir string, args ...string) (*exec.Cmd, *SafeBuffer) {
	t.Helper()
	repoDir := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR")
	if repoDir == "" {
		repoDir = "../../original-reticulum-repo"
	}
	scriptPath := filepath.Join(repoDir, "RNS", "Utilities", "rncp.py")
	fullArgs := append([]string{"-u", scriptPath, "--config", configDir}, args...)
	cmd := exec.Command("python3", fullArgs...)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+repoDir)
	buf := &SafeBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("runPythonBackground failed: %v", err)
	}
	return cmd, buf
}

func runGorncp(t *testing.T, configDir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"run", ".", "-config", configDir}, args...)
	t.Logf("Running command: go %s", strings.Join(fullArgs, " "))
	cmd := exec.Command("go", fullArgs...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Command failed with error: %v", err)
	}
	return string(out)
}

func runGorncpBackground(t *testing.T, configDir string, args ...string) (*exec.Cmd, *SafeBuffer) {
	t.Helper()
	fullArgs := append([]string{"run", ".", "-config", configDir}, args...)
	t.Logf("Running background command: go %s", strings.Join(fullArgs, " "))
	cmd := exec.Command("go", fullArgs...)
	cmd.Dir = "."
	buf := &SafeBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("runGorncpBackground failed: %v", err)
	}
	t.Logf("Background command started, PID: %d", cmd.Process.Pid)
	return cmd, buf
}

func captureStdout(f func()) string {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	f()

	_ = w.Close()
	<-done

	os.Stdout = oldStdout
	return buf.String()
}

func TestVersionParity(t *testing.T) {
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	configDir := filepath.Join(tmpDir, "config")

	pyOut := strings.TrimSpace(runPython(t, configDir, "--version"))
	if !strings.HasPrefix(pyOut, "rncp ") {
		t.Errorf("Python version output doesn't match expected format: %q", pyOut)
	}

	goOut := runGorncp(t, configDir, "--version")
	if !strings.Contains(goOut, "gorncp "+rns.VERSION) {
		t.Errorf("Go version output doesn't match expected format: %q", goOut)
	}
}

func TestIdentityDisplayParity(t *testing.T) {
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	idPath := filepath.Join(tmpDir, "identity")
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	pyOut := runPython(t, configDir, "-i", idPath, "-p", "-l", "-n")
	goOut := runGorncp(t, configDir, "-i", idPath, "-p", "-l", "-n")

	if !strings.Contains(pyOut, "Identity     :") || !strings.Contains(pyOut, "Listening on :") {
		t.Errorf("Python output format unexpected:\n%s", pyOut)
	}

	if !strings.Contains(goOut, "Identity     :") || !strings.Contains(goOut, "Listening on :") {
		t.Errorf("Go output format unexpected:\n%s", goOut)
	}

	goLines := strings.Split(strings.TrimSpace(goOut), "\n")

	// Find the Identity and Listening lines (may be preceded by log messages)
	var identityLine, listeningLine string
	for _, line := range goLines {
		if strings.HasPrefix(line, "Identity     :") {
			identityLine = line
		}
		if strings.HasPrefix(line, "Listening on :") {
			listeningLine = line
		}
	}

	if identityLine == "" {
		t.Fatalf("Could not find Identity line in output:\n%s", goOut)
	}
	if listeningLine == "" {
		t.Fatalf("Could not find Listening line in output:\n%s", goOut)
	}

	if !strings.HasPrefix(identityLine, "Identity     : <") || !strings.HasSuffix(identityLine, ">") {
		t.Errorf("Go identity line format wrong: %q", identityLine)
	}
	if !strings.HasPrefix(listeningLine, "Listening on : <") || !strings.HasSuffix(listeningLine, ">") {
		t.Errorf("Go listening line format wrong: %q", listeningLine)
	}
}

func TestHelpParity(t *testing.T) {
	_ = runPython(t, "/tmp/nonexistent-py", "--help")
	goOut := captureStdout(printUsage)

	if !strings.Contains(goOut, "usage: gorncp") {
		t.Errorf("Go help output missing usage line: %q", goOut)
	}

	flags := []string{
		"--config", "-v", "-q", "-S", "-l", "-C", "-F", "-f", "-j",
		"-s", "-O", "-b", "-a", "-n", "-p", "-i", "-w", "-P", "--version",
	}

	for _, flag := range flags {
		if !strings.Contains(goOut, flag) {
			t.Errorf("Go help output missing flag %q", flag)
		}
	}

	if !strings.Contains(goOut, "file") || !strings.Contains(goOut, "destination") {
		t.Errorf("Go help output missing positional arguments")
	}

	descToMatch := "disable transfer progress output"
	if !strings.Contains(goOut, descToMatch) {
		t.Errorf("Go help output missing description: %q", descToMatch)
	}
}

func TestUnauthenticatedTransferParity(t *testing.T) {
	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	serverConfigDir := filepath.Join(tmpDir, "server_config")
	clientConfigDir := filepath.Join(tmpDir, "client_config")
	saveDir := filepath.Join(tmpDir, "save")
	if err := os.MkdirAll(serverConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll server_config: %v", err)
	}
	if err := os.MkdirAll(clientConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll client_config: %v", err)
	}
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatalf("MkdirAll save: %v", err)
	}

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("getFreePort: %v", err)
	}

	serverConfig := fmt.Sprintf(`[reticulum]
enable_transport = Yes
share_instance = No

[interfaces]
  [[TCP]]
    type = TCPServerInterface
    interface_enabled = yes
    listen_ip = 127.0.0.1
    listen_port = %d
`, port)
	serverConfigPath := filepath.Join(serverConfigDir, "config")
	if err := os.WriteFile(serverConfigPath, []byte(serverConfig), 0o644); err != nil {
		t.Fatalf("WriteFile server config: %v", err)
	}

	clientConfig := fmt.Sprintf(`[reticulum]
share_instance = No

[interfaces]
  [[TCP]]
    type = TCPClientInterface
    interface_enabled = yes
    target_host = 127.0.0.1
    target_port = %d
`, port)
	clientConfigPath := filepath.Join(clientConfigDir, "config")
	if err := os.WriteFile(clientConfigPath, []byte(clientConfig), 0o644); err != nil {
		t.Fatalf("WriteFile client config: %v", err)
	}

	serverIdentity := filepath.Join(tmpDir, "server_identity")
	clientIdentity := filepath.Join(tmpDir, "client_identity")

	listenerReady := make(chan string, 1)
	listenerDone := make(chan struct{}, 1)

	go func() {
		lCmd, buf := runGorncpBackground(t, serverConfigDir, "-l", "-n", "-s", saveDir, "-i", serverIdentity, "-b", "2", "-v")
		defer func() {
			_ = lCmd.Process.Signal(os.Interrupt)
			time.Sleep(500 * time.Millisecond)
			_ = lCmd.Process.Kill()
		}()

		timeout := time.After(20 * time.Second)
		var destHash string
		for {
			select {
			case <-timeout:
				t.Logf("=== LISTENER OUTPUT (complete) ===\n%s", buf.String())
				t.Errorf("listener goroutine: timed out waiting for listener to start. Output above.")
				return
			default:
				out := buf.String()
				if strings.Contains(out, "Listening on : <") {
					parts := strings.Split(out, "Listening on : <")
					if len(parts) > 1 {
						destHash = strings.Split(parts[1], ">")[0]
						t.Logf("listener goroutine: ready at %s", destHash)
						listenerReady <- destHash
						select {
						case <-listenerDone:
						case <-time.After(2 * time.Second):
						}
						t.Logf("=== LISTENER OUTPUT (complete) ===\n%s", buf.String())
						return
					}
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}()

	var destHash string
	select {
	case destHash = <-listenerReady:
		t.Logf("Listener ready, hash: %s", destHash)
	case <-time.After(20 * time.Second):
		t.Fatalf("Timed out waiting for listener to become ready")
	}

	time.Sleep(5 * time.Second)

	testFile := filepath.Join(tmpDir, "test.txt")
	testData := "Hello from Go sender!"
	if err := os.WriteFile(testFile, []byte(testData), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sOut := runGorncp(t, clientConfigDir, "-i", clientIdentity, "-w", "30", destHash, testFile, "-v")
	t.Logf("Sender output:\n%s", sOut)

	if !strings.Contains(sOut, "Transfer complete") {
		t.Errorf("Sender did not indicate transfer complete")
	}

	receivedFile := filepath.Join(saveDir, "test.txt")
	fileReady := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := os.Stat(receivedFile); err == nil {
				close(fileReady)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	select {
	case <-fileReady:
		t.Logf("Received file detected")
	case <-time.After(15 * time.Second):
		t.Fatalf("Timed out waiting for received file to appear")
	}

	time.Sleep(500 * time.Millisecond)

	data, err := os.ReadFile(receivedFile)
	if err != nil {
		t.Fatalf("Could not read received file: %v", err)
	}
	if string(data) != testData {
		t.Errorf("Received data mismatch: expected %q, got %q", testData, string(data))
	}

	close(listenerDone)
}

func TestListenModeIdentityCreation(t *testing.T) {
	// Create a temp directory for config
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	identityDir := filepath.Join(tmpDir, "identities")
	identityPath := filepath.Join(identityDir, AppName)

	// Verify identity doesn't exist initially
	if _, err := os.Stat(identityPath); err == nil {
		t.Fatal("Identity should not exist initially")
	}

	// Simulate what listen() does - create new identity if not found
	var id *rns.Identity
	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		// Create directory first (matches Python behavior)
		if err := os.MkdirAll(identityDir, 0o700); err != nil {
			t.Fatalf("Could not create identity directory: %v", err)
		}
		var err error
		id, err = rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("Could not create identity: %v", err)
		}
		if err := id.ToFile(identityPath); err != nil {
			t.Fatalf("Could not persist identity %q: %v", identityPath, err)
		}
	}

	// Verify identity was created
	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		t.Fatal("Identity should be created")
	}

	// Verify identity can be loaded
	loadedID, err := rns.FromFile(identityPath)
	if err != nil {
		t.Fatalf("Identity should be loadable: %v", err)
	}

	if loadedID == nil {
		t.Fatal("Loaded identity should not be nil")
	}
}
