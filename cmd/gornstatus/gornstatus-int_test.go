// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

const pyServerScript = `
import RNS
import time
import sys
import os

config_dir = sys.argv[1]
allowed_identity_path = sys.argv[2]

reticulum = RNS.Reticulum(configdir=config_dir)
RNS.logdest = RNS.LOG_STDOUT
RNS.loglevel = RNS.LOG_DEBUG

identity = RNS.Identity.from_file(config_dir + "/storage/transport_identity")
dest = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "rnstransport", "remote", "management")

allowed_identity = RNS.Identity.from_file(allowed_identity_path)
allowed_list = [allowed_identity.hash]

def status_request_handler(path, data, request_id, link_id, remote_identity, requested_at):
    print(f"DEBUG: Received request for {path} from {RNS.prettyhexrep(remote_identity.hash)}")
    stats = {
        "transport_id": RNS.Transport.identity.hash,
        "interfaces": [
            {"name": "Mock Interface", "type": "Test", "status": True, "bitrate": 1000, "rxb": 123, "txb": 456}
        ]
    }
    return [stats, 5]

dest.register_request_handler("/status", status_request_handler, allow=RNS.Destination.ALLOW_LIST, allowed_list=allowed_list)

print(f"DEBUG: Server starting with identity {RNS.prettyhexrep(identity.hash)}")
print(f"DEBUG: Management destination {RNS.prettyhexrep(dest.hash)}")
print(f"DEBUG: Allowing identity {RNS.prettyhexrep(allowed_identity.hash)}")
print("DEBUG: Server ready and waiting")

while True:
    dest.announce()
    print("DEBUG: Sent announcement")
    time.sleep(5)
`

const pyAnnounceScript = `
import RNS
import time
import sys
import os

config_dir = sys.argv[1]
reticulum = RNS.Reticulum(configdir=config_dir)
time.sleep(2)

identity = RNS.Identity.from_file(config_dir + "/storage/transport_identity")
dest = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "rnstransport", "remote", "management")

print(f"DEBUG: Announcing management destination {RNS.prettyhexrep(dest.hash)} repeatedly...")
for _ in range(15):
    dest.announce()
    time.sleep(2)
`

func buildGornstatus(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	bin := filepath.Join(tmpDir, "gornstatus")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		t.Fatalf("failed to build gornstatus: %v\n%v", err, string(out))
	}
	return bin, cleanup
}

func TestIntegration_VersionOutput(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	out, err := exec.Command(bin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornstatus --version failed: %v\n%v", err, string(out))
	}
	want := "gornstatus " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestIntegration_HelpOutput(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	out, err := exec.Command(bin, "--help").CombinedOutput()
	_ = err
	output := string(out)
	for _, want := range []string{
		"Reticulum Network Stack Status",
		"--config",
		"--version",
		"-a, --all",
		"-A, --announce-stats",
		"-l, --link-stats",
		"-t, --totals",
		"-s SORT, --sort SORT",
		"-r, --reverse",
		"-j, --json",
		"-R hash",
		"-i path",
		"-w seconds",
		"-d, --discovered",
		"-m, --monitor",
		"-I seconds, --monitor-interval seconds",
		"-v, --verbose",
		"filter",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("help output missing %q, got:\n%v", want, output)
		}
	}
}

func TestIntegration_ExitCodeZero(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	tmpDir, cleanup := testutils.TempDirWithConfig(t, tempDirPrefix, func(dir string) string {
		instanceName := filepath.Base(dir)
		return "[reticulum]\nenable_transport = False\nshare_instance = Yes\ninstance_name = " + instanceName + "\n\n[logging]\nloglevel = 2\n"
	})
	defer cleanup()
	cmd := exec.Command(bin, "--config", tmpDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gornstatus exited with error: %v\n%v", err, string(out))
	}
}

func TestIntegration_SIGINTCleanExit(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	tmpDir, cleanup := testutils.TempDirWithConfig(t, tempDirPrefix, func(dir string) string {
		instanceName := filepath.Base(dir)
		return "[reticulum]\nenable_transport = False\nshare_instance = Yes\ninstance_name = " + instanceName + "\n\n[logging]\nloglevel = 2\n"
	})
	defer cleanup()
	cmd := exec.Command(bin, "--config", tmpDir, "-m", "-I", "10")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGINT)
	err := cmd.Wait()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() > 1 {
			t.Errorf("expected clean exit, got: %v", err)
		}
	}
}

func TestIntegration_MonitorModeSIGINT(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	tmpDir, cleanup := testutils.TempDirWithConfig(t, tempDirPrefix, func(dir string) string {
		instanceName := filepath.Base(dir)
		return "[reticulum]\nenable_transport = False\nshare_instance = Yes\ninstance_name = " + instanceName + "\n\n[logging]\nloglevel = 2\n"
	})
	defer cleanup()
	cmd := exec.Command(bin, "--config", tmpDir, "-m", "-I", "0.1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGINT)
	err := cmd.Wait()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() > 1 {
			t.Errorf("expected clean exit, got: %v", err)
		}
	}
}

func TestIntegration_VerboseStacking(t *testing.T) {
	t.Parallel()
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()
	out, err := exec.Command(bin, "-v", "-v", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("gornstatus -v -v --version failed: %v\n%v", err, string(out))
	}
	want := "gornstatus " + rns.VERSION
	got := strings.TrimSpace(string(out))
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestIntegration_RemoteStatus(t *testing.T) {
	testutils.SkipShortIntegration(t)
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()

	// 1. Setup Python Node (Server)
	pyConfigDir, cleanupPy := testutils.TempDir(t, "gornstatus-py-server-")
	defer cleanupPy()
	pyInstanceName := "gornstatus-py-server-" + filepath.Base(pyConfigDir)

	mgmtIDPath := filepath.Join(pyConfigDir, "management.id")
	mgmtID, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgmtID.ToFile(mgmtIDPath); err != nil {
		t.Fatal(err)
	}

	pyServerPath := filepath.Join(pyConfigDir, "py_server.py")
	if err := os.WriteFile(pyServerPath, []byte(pyServerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	pyAnnouncePath := filepath.Join(pyConfigDir, "py_announce.py")
	if err := os.WriteFile(pyAnnouncePath, []byte(pyAnnounceScript), 0o755); err != nil {
		t.Fatal(err)
	}

	listenPort := reserveUDPPort(t)
	forwardPort := reserveUDPPort(t)

	pyConfig := strings.Join([]string{
		"[reticulum]",
		"enable_transport = Yes",
		"share_instance = No",
		"instance_name = " + pyInstanceName,
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
		"[remote_management]",
		"  enabled = Yes",
		"  allowed_identities = " + mgmtID.HexHash,
	}, "\n")
	if err := os.WriteFile(filepath.Join(pyConfigDir, "config"), []byte(pyConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	pyCmd := exec.Command("python3", "-u", pyServerPath, pyConfigDir, mgmtIDPath)
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	pyOut := &safeBuffer{}
	pyCmd.Stdout = pyOut
	pyCmd.Stderr = pyOut
	if err := pyCmd.Start(); err != nil {
		t.Fatalf("failed to start Python RNS: %v", err)
	}
	defer pyCmd.Process.Kill()

	// Wait for Python to start and print "ready"
	var serverHash string
	var managementHash string
	start := time.Now()
	for time.Since(start) < 30*time.Second {
		out := pyOut.String()
		if strings.Contains(out, "DEBUG: Server starting") && serverHash == "" {
			lines := strings.Split(out, "\n")
			for _, line := range lines {
				if strings.Contains(line, "DEBUG: Server starting") {
					parts := strings.Split(line, "identity <")
					if len(parts) > 1 {
						serverHash = strings.Split(parts[1], ">")[0]
					}
				}
			}
		}
		if strings.Contains(out, "DEBUG: Management destination") && managementHash == "" {
			lines := strings.Split(out, "\n")
			for _, line := range lines {
				if strings.Contains(line, "DEBUG: Management destination") {
					parts := strings.Split(line, "destination <")
					if len(parts) > 1 {
						managementHash = strings.Split(parts[1], ">")[0]
					}
				}
			}
		}
		if serverHash != "" && managementHash != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if serverHash == "" || managementHash == "" {
		t.Fatalf("timed out waiting for Python server hashes\nserverHash=%q, managementHash=%q\noutput:\n%v", serverHash, managementHash, pyOut.String())
	}
	t.Logf("Python server hash: %v", serverHash)
	t.Logf("Python management hash: %v", managementHash)

	// 2. Setup Go Node (Initiator)
	goConfigDir, cleanupGo := testutils.TempDir(t, "gornstatus-go-client-")
	defer cleanupGo()
	goInstanceName := "gornstatus-go-client-" + filepath.Base(goConfigDir)

	goConfig := strings.Join([]string{
		"[reticulum]",
		"enable_transport = Yes",
		"share_instance = No",
		"instance_name = " + goInstanceName,
		"",
		"[interfaces]",
		"  [[Default Interface]]",
		"    type = UDPInterface",
		"    enabled = Yes",
		"    listen_ip = 127.0.0.1",
		"    listen_port = " + fmt.Sprintf("%v", forwardPort),
		"    forward_ip = 127.0.0.1",
		"    forward_port = " + fmt.Sprintf("%v", listenPort),
	}, "\n")
	if err := os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	// Trigger an announcement from Python so Go sees it
	announceCmd := exec.Command("python3", pyAnnouncePath, pyConfigDir)
	announceCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
	if err := announceCmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer announceCmd.Process.Kill()
	time.Sleep(10 * time.Second)

	// Run gornstatus -R using managementHash
	cmd := exec.Command(bin, "--config", goConfigDir, "-R", managementHash, "-i", mgmtIDPath, "-w", "20", "-v")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gornstatus -R failed: %v\noutput:\n%v\nPython output:\n%v", err, string(out), pyOut.String())
	}

	got := string(out)
	if !strings.Contains(got, "Transport Instance <"+serverHash+"> running") {
		t.Errorf("output missing server status\ngot:\n%v", got)
	}
}

func TestIntegration_Discovered(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	bin, cleanupBin := buildGornstatus(t)
	defer cleanupBin()

	tmpDir, cleanup := testutils.TempDir(t, "gornstatus-int-disc-")
	defer cleanup()

	// Setup mock discovery data in shared storage
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	mockData := map[string]any{
		"name":       "Discovery Test Interface",
		"type":       "UDPInterface",
		"last_heard": now - 30,
		"value":      999,
	}
	data, err := msgpack.Pack(mockData)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storagePath, "disc.data"), data, 0o644); err != nil {
		t.Fatalf("failed to write mock data: %v", err)
	}

	// We need a Reticulum instance to provide the config directory
	// but gornstatus -d will create one.
	cmd := exec.Command(bin, "--config", tmpDir, "-d")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gornstatus -d failed: %v\noutput:\n%v", err, string(out))
	}

	got := string(out)
	if !strings.Contains(got, "Discovery Test Interface") {
		t.Errorf("output missing Discovery Test Interface\ngot:\n%v", got)
	}
	if !strings.Contains(got, "UDP") {
		t.Errorf("output missing UDP type\ngot:\n%v", got)
	}
}

func getPythonPath() string {
	if path := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR"); path != "" {
		return path
	}
	return ""
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

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
