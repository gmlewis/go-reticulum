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

type nilFetchLinkResolver struct{}

func (nilFetchLinkResolver) FindLink([]byte) *rns.Link {
	return nil
}

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

func TestFetchFileNotFoundOnRemote(t *testing.T) {
	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	serverConfigDir := filepath.Join(tmpDir, "server_config")
	clientConfigDir := filepath.Join(tmpDir, "client_config")
	if err := os.MkdirAll(serverConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll server_config: %v", err)
	}
	if err := os.MkdirAll(clientConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll client_config: %v", err)
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
		lCmd, buf := runGorncpBackground(t, serverConfigDir, "-l", "-n", "-F", "-i", serverIdentity, "-b", "2", "-v")
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
				t.Errorf("listener goroutine: timed out waiting for listener to start.")
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

	time.Sleep(2 * time.Second)

	fOut := runGorncp(t, clientConfigDir, "-i", clientIdentity, "-w", "30", "-f", destHash, "nonexistent_file.txt", "-v")
	t.Logf("Fetcher output:\n%s", fOut)

	if !strings.Contains(fOut, "was not found on the remote") {
		t.Errorf("Fetcher output does not contain expected error message")
	}

	close(listenerDone)
}

func TestFetchPathLookupTimeout(t *testing.T) {
	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll config: %v", err)
	}

	configPath := filepath.Join(configDir, "config")
	configData := "[reticulum]\nshare_instance = No\nenable_transport = No\n"
	if err := os.WriteFile(configPath, []byte(configData), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	binaryPath := filepath.Join(tmpDir, "gorncp")
	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	buildCmd.Dir = "."
	buildCmd.Env = append(os.Environ(), "HOME="+tmpDir)
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("go build failed: %v", err)
	}

	destHash := strings.Repeat("f", (rns.TruncatedHashLength/8)*2)
	cmd := exec.Command(binaryPath, "-config", configDir, "-f", destHash, "missing.txt", "-w", "1", "-q")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected fetch path lookup timeout to fail, output: %s", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %T: %v", err, err)
	}
	if got := exitErr.ExitCode(); got != 1 {
		t.Fatalf("exit code = %d, want 1; output: %s", got, string(out))
	}
	expectedMsg := fmt.Sprintf("Path %q not found", destHash)
	if !strings.Contains(string(out), expectedMsg) {
		t.Fatalf("output does not contain %q: %s", expectedMsg, string(out))
	}
}

func TestFetchLinkEstablishmentTimeout(t *testing.T) {
	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll config: %v", err)
	}
	configPath := filepath.Join(configDir, "config")
	configData := "[reticulum]\nshare_instance = No\nenable_transport = No\n"
	if err := os.WriteFile(configPath, []byte(configData), 0o644); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}

	seedTS := rns.NewTransportSystem()
	seedID, err := rns.NewIdentity(true)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	seedDest, err := rns.NewDestination(seedTS, seedID, rns.DestinationIn, rns.DestinationSingle, AppName, "receive")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	seedTS.Remember([]byte("seed-packet"), seedDest.Hash, seedID.GetPublicKey(), nil)
	seedTS.SaveKnownDestinations(filepath.Join(configDir, "storage"))

	binaryPath := filepath.Join(tmpDir, "gorncp")
	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	buildCmd.Dir = "."
	buildCmd.Env = append(os.Environ(), "HOME="+tmpDir)
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("go build failed: %v", err)
	}

	identityPath := filepath.Join(tmpDir, "identity")
	cmd := exec.Command(binaryPath, "-config", configDir, "-f", seedDest.HexHash, "missing.txt", "-w", "1", "-q", "-i", identityPath)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected link establishment timeout to fail, output: %s", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %T: %v", err, err)
	}
	if got := exitErr.ExitCode(); got != 1 {
		t.Fatalf("exit code = %d, want 1; output: %s", got, string(out))
	}
	if !strings.Contains(string(out), "Link establishment timed out") {
		t.Fatalf("output does not contain %q: %s", "Link establishment timed out", string(out))
	}
}

func TestFetchRequestTimeout(t *testing.T) {
	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	serverConfigDir := filepath.Join(tmpDir, "server_config")
	clientConfigDir := filepath.Join(tmpDir, "client_config")
	if err := os.MkdirAll(serverConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll server_config: %v", err)
	}
	if err := os.MkdirAll(clientConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll client_config: %v", err)
	}

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("getFreePort: %v", err)
	}

	listenerIdentityPath := filepath.Join(tmpDir, "listener_identity")
	serverConfig := fmt.Sprintf(`[reticulum]
share_instance = No
enable_transport = Yes
network_identity = %v

[interfaces]
  [[TCP]]
    type = TCPServerInterface
    interface_enabled = yes
    listen_ip = 127.0.0.1
    listen_port = %d
`, listenerIdentityPath, port)
	if err := os.WriteFile(filepath.Join(serverConfigDir, "config"), []byte(serverConfig), 0o644); err != nil {
		t.Fatalf("WriteFile server config: %v", err)
	}

	clientConfig := fmt.Sprintf(`[reticulum]
share_instance = No
enable_transport = Yes

[interfaces]
  [[TCP]]
    type = TCPClientInterface
    interface_enabled = yes
    target_host = 127.0.0.1
    target_port = %d
`, port)
	if err := os.WriteFile(filepath.Join(clientConfigDir, "config"), []byte(clientConfig), 0o644); err != nil {
		t.Fatalf("WriteFile client config: %v", err)
	}

	listenerStack, err := rns.NewReticulum(rns.NewTransportSystem(), serverConfigDir)
	if err != nil {
		t.Fatalf("NewReticulum listener: %v", err)
	}
	defer func() { _ = listenerStack.Close() }()

	listenerID, err := rns.FromFile(listenerIdentityPath)
	if err != nil {
		t.Fatalf("FromFile listener identity: %v", err)
	}
	listenerDest, err := rns.NewDestination(listenerStack.Transport(), listenerID, rns.DestinationIn, rns.DestinationSingle, AppName, "receive")
	if err != nil {
		t.Fatalf("NewDestination listener: %v", err)
	}
	_ = listenerDest

	clientSeed := rns.NewTransportSystem()
	clientSeed.Remember([]byte("seed-request"), listenerDest.Hash, listenerID.GetPublicKey(), nil)
	clientSeed.SaveKnownDestinations(filepath.Join(clientConfigDir, "storage"))

	_ = mustCreateIdentity(t, clientConfigDir, "client")
	clientIdentityPath := filepath.Join(clientConfigDir, "identities", "client")

	binaryPath := filepath.Join(tmpDir, "gorncp")
	buildCmd := exec.Command("go", "build", "-o", binaryPath, ".")
	buildCmd.Dir = "."
	buildCmd.Env = append(os.Environ(), "HOME="+tmpDir)
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("go build failed: %v", err)
	}

	cmd := exec.Command(binaryPath, "-config", clientConfigDir, "-i", clientIdentityPath, "-q", "-f", listenerDest.HexHash, "testfile.txt")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)
	buf := &SafeBuffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fetch timeout command: %v", err)
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		waitErr = <-done
	}

	out := buf.String()
	if waitErr == nil {
		t.Fatalf("expected fetch request timeout to fail, output: %s", out)
	}
	exitErr, ok := waitErr.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %T: %v", waitErr, waitErr)
	}
	if got := exitErr.ExitCode(); got != 1 {
		t.Fatalf("exit code = %d, want 1; output: %s", got, out)
	}
	if !strings.Contains(out, "Fetch request timed out") {
		t.Fatalf("output does not contain %q: %s", "Fetch request timed out", out)
	}
}

func TestFetchNotAllowedByRemote(t *testing.T) {
	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	serverConfigDir := filepath.Join(tmpDir, "server_config")
	clientConfigDir := filepath.Join(tmpDir, "client_config")
	if err := os.MkdirAll(serverConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll server_config: %v", err)
	}
	if err := os.MkdirAll(clientConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll client_config: %v", err)
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
		lCmd, buf := runGorncpBackground(t, serverConfigDir, "-l", "-n", "-i", serverIdentity, "-b", "2", "-v")
		defer func() {
			_ = lCmd.Process.Signal(os.Interrupt)
			time.Sleep(500 * time.Millisecond)
			_ = lCmd.Process.Kill()
			t.Logf("=== LISTENER OUTPUT (complete) ===\n%s", buf.String())
		}()

		timeout := time.After(20 * time.Second)
		var destHash string
		for {
			select {
			case <-timeout:
				t.Logf("=== LISTENER OUTPUT (timeout) ===\n%s", buf.String())
				t.Errorf("listener goroutine: timed out waiting for listener to start.")
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

	time.Sleep(2 * time.Second)

	testFile := filepath.Join(tmpDir, "test.txt")
	testData := "Test file for fetch"
	if err := os.WriteFile(testFile, []byte(testData), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fOut := runGorncp(t, clientConfigDir, "-i", clientIdentity, "-w", "30", "-f", destHash, testFile, "-v")
	t.Logf("Fetcher output:\n%s", fOut)

	if !strings.Contains(fOut, "was not allowed by the remote") {
		t.Errorf("Fetcher output does not contain expected error message")
	}

	close(listenerDone)
}

func TestFetchRemoteErrorWhenLinkIsMissing(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("Test file for fetch"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	handler := newFetchRequestHandler(true, "", false, nilFetchLinkResolver{})
	response := handler("fetch_file", []byte(testFile), []byte("request-id"), []byte("link-id"), nil, time.Now())

	if response != nil {
		t.Fatalf("response=%v want=nil", response)
	}
}

func TestFetchUnknownUnauthorizedError(t *testing.T) {
	t.Parallel()

	rr := &rns.RequestReceipt{Status: rns.RequestSent}
	msg := getFetchErrorMessage(rr, "testfile.txt")

	if !strings.Contains(msg, "due to an unknown error (probably not authorised)") {
		t.Fatalf("message=%q does not contain expected unknown-error text", msg)
	}
}
