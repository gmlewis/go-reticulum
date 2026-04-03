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

var gorncpBin string

func TestMain(m *testing.M) {
	// Build gorncp once for the whole suite
	tmpDir, err := os.MkdirTemp("", "gorncp-test-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for build: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binPath := filepath.Join(tmpDir, "gorncp")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go build failed: %v\nOutput: %s\n", err, string(out))
		os.Exit(1)
	}
	gorncpBin = binPath

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
	fullArgs := append([]string{"-config", configDir}, args...)
	t.Logf("Running command: %s %s", gorncpBin, strings.Join(fullArgs, " "))
	cmd := exec.Command(gorncpBin, fullArgs...)
	out, err := cmd.CombinedOutput()
	_ = err
	return string(out)
}

func runGorncpBackground(t *testing.T, configDir string, args ...string) (*exec.Cmd, *SafeBuffer) {
	t.Helper()
	fullArgs := append([]string{"-config", configDir}, args...)
	t.Logf("Running background command: %s %s", gorncpBin, strings.Join(fullArgs, " "))
	cmd := exec.Command(gorncpBin, fullArgs...)
	buf := &SafeBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("runGorncpBackground failed: %v", err)
	}
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
	// Listener: Go, Sender: Go
	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	serverConfigDir := filepath.Join(tmpDir, "server_config")
	clientConfigDir := filepath.Join(tmpDir, "client_config")
	saveDir := filepath.Join(tmpDir, "save")
	os.MkdirAll(serverConfigDir, 0o755)
	os.MkdirAll(clientConfigDir, 0o755)
	os.MkdirAll(saveDir, 0o755)

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("getFreePort: %v", err)
	}

	serverConfig := fmt.Sprintf(`[reticulum]
enable_transport = Yes
share_instance = No

[interfaces]
  [[TCP]]
    type = TCPInterface
    interface_enabled = yes
    listen_ip = 127.0.0.1
    listen_port = %d
`, port)
	serverConfigPath := filepath.Join(serverConfigDir, "config")
	if err := os.WriteFile(serverConfigPath, []byte(serverConfig), 0o644); err != nil {
		t.Fatalf("WriteFile server config: %v", err)
	}
	t.Logf("Wrote server config to %s", serverConfigPath)

	clientConfig := fmt.Sprintf(`[reticulum]
share_instance = No

[interfaces]
  [[TCP]]
    type = TCPInterface
    interface_enabled = yes
    target_host = 127.0.0.1
    target_port = %d
`, port)
	clientConfigPath := filepath.Join(clientConfigDir, "config")
	if err := os.WriteFile(clientConfigPath, []byte(clientConfig), 0o644); err != nil {
		t.Fatalf("WriteFile client config: %v", err)
	}
	t.Logf("Wrote client config to %s", clientConfigPath)

	serverIdentity := filepath.Join(tmpDir, "server_identity")
	clientIdentity := filepath.Join(tmpDir, "client_identity")

	// Start Go listener
	lCmd, lBuf := runGorncpBackground(t, serverConfigDir, "-l", "-n", "-s", saveDir, "-i", serverIdentity, "-b", "2", "-v")
	defer func() {
		lCmd.Process.Kill()
		t.Logf("Listener output:\n%s", lBuf.String())
	}()

	// Wait for listener to be ready and get its hash
	var destHash string
	timeout := time.After(15 * time.Second)
	for {
		select {
		case <-timeout:
			t.Fatalf("timed out waiting for listener to start. Output:\n%s", lBuf.String())
		default:
			out := lBuf.String()
			if strings.Contains(out, "Listening on : <") {
				parts := strings.Split(out, "Listening on : <")
				if len(parts) > 1 {
					destHash = strings.Split(parts[1], ">")[0]
					goto listenerReady
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
listenerReady:
	t.Logf("Listener ready at %s", destHash)
	time.Sleep(5 * time.Second) // Let announce propagate (interval is 2 seconds)

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.txt")
	testData := "Hello from Go sender!"
	if err := os.WriteFile(testFile, []byte(testData), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Start Go sender
	sOut := runGorncp(t, clientConfigDir, "-i", clientIdentity, "-w", "30", destHash, testFile, "-v")
	t.Logf("Sender output:\n%s", sOut)

	if !strings.Contains(sOut, "Transfer complete") {
		t.Errorf("Sender did not indicate transfer complete")
	}

	// Verify file received
	receivedFile := filepath.Join(saveDir, "test.txt")
	// Wait a bit for file to be written
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(receivedFile); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	data, err := os.ReadFile(receivedFile)
	if err != nil {
		t.Fatalf("Could not read received file: %v", err)
	}
	if string(data) != testData {
		t.Errorf("Received data mismatch: expected %q, got %q", testData, string(data))
	}
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
