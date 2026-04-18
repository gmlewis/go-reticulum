// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package interfaces

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"crypto/sha256"
	"encoding/hex"

	"github.com/gmlewis/go-reticulum/testutils"
)

const pythonDiscoveryTokenScript = `
import hashlib
import sys

group_id = "reticulum".encode("utf-8")
link_local_address = sys.argv[1]
discovery_token = hashlib.sha256(group_id + link_local_address.encode("utf-8")).digest()
print(discovery_token.hex())
`

func TestAutoInterfaceDiscoveryPacketParity(t *testing.T) {
	testutils.SkipShortIntegration(t)
	pythonPath := getPythonPath()
	tmpDir, cleanup := testutils.TempDir(t, "rns-auto-parity-*")
	defer cleanup()

	scriptPath := filepath.Join(tmpDir, "discovery_token.py")
	if err := os.WriteFile(scriptPath, []byte(pythonDiscoveryTokenScript), 0o644); err != nil {
		t.Fatal(err)
	}

	testAddresses := []string{
		"fe80::1",
		"fe80::dead:beef:face:b00c",
		"fe80::215:5dff:fe00:1db1",
	}

	for _, addr := range testAddresses {
		t.Run(addr, func(t *testing.T) {
			// Get Python's token
			cmd := exec.Command("python3", scriptPath, addr)
			cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)
			pyOut, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("python script failed: %v\n%s", err, string(pyOut))
			}
			pyHex := strings.TrimSpace(string(pyOut))

			// Calculate Go's token
			// Logic from rns/interfaces/auto.go: peerAnnounce
			// token := sha256.Sum256(append(append([]byte{}, ai.groupID...), []byte(localIP.String())...))
			groupID := []byte("reticulum")
			ip := net.ParseIP(addr)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", addr)
			}
			goToken := sha256.Sum256(append(append([]byte{}, groupID...), []byte(ip.String())...))
			goHex := hex.EncodeToString(goToken[:])

			if goHex != pyHex {
				t.Errorf("token mismatch for address %q\nGo:     %s\nPython: %s", addr, goHex, pyHex)
			}
		})
	}
}

func getPythonPath() string {
	if path := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR"); path != "" {
		return path
	}
	log.Fatalf("missing required environment variable: ORIGINAL_RETICULUM_REPO_DIR")
	return ""
}

func requirePythonModule(t *testing.T, module string) {
	t.Helper()
	cmd := exec.Command("python3", "-c", "import importlib.util,sys; sys.exit(0 if importlib.util.find_spec(sys.argv[1]) else 1)", module)
	if err := cmd.Run(); err != nil {
		t.Skipf("skipping integration test: python module %q is not available", module)
	}
}

const pythonUDPEchoScript = `
import RNS.Interfaces.UDPInterface as UDPInterface
import time
import sys
import os

class Owner:
    def inbound(self, data, interface):
        # Echo back
        interface.process_outgoing(data)

config = {
    "name": "test_udp",
    "listen_ip": "127.0.0.1",
    "listen_port": int(sys.argv[1]),
    "forward_ip": "127.0.0.1",
    "forward_port": int(sys.argv[2])
}

owner = Owner()
iface = UDPInterface.UDPInterface(owner, config)

# Keep alive
try:
    while True:
        time.sleep(1)
except KeyboardInterrupt:
    pass
`

func TestUDPInterfaceParity(t *testing.T) {
	testutils.SkipShortIntegration(t)
	pythonPath := getPythonPath()
	tmpDir, cleanup := testutils.TempDir(t, "rns-udp-parity-*")
	defer cleanup()

	scriptPath := filepath.Join(tmpDir, "udp_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonUDPEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	p1, p2 := allocateUDPPortPair(t)
	pyListenPort := p1
	goListenPort := p2

	cmd := exec.Command("python3", scriptPath, fmt.Sprintf("%v", pyListenPort), fmt.Sprintf("%v", goListenPort))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start Python UDP echo: %v", err)
	}
	t.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil {
			t.Logf("failed to kill Python UDP echo: %v", err)
		}
	})

	// Wait for Python to start
	time.Sleep(500 * time.Millisecond)

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		select {
		case received <- data:
		default:
		}
	}

	goIface := mustTestNewUDPInterface(t, "go_udp", "127.0.0.1", goListenPort, "127.0.0.1", pyListenPort, handler)
	t.Cleanup(func() {
		if err := goIface.Detach(); err != nil {
			t.Logf("failed to detach Go UDP interface: %v", err)
		}
	})

	msg := []byte("hello from go to python")
	deadline := time.After(10 * time.Second)
	for {
		if err := goIface.Send(msg); err != nil {
			t.Fatalf("failed to send data to Python: %v", err)
		}

		select {
		case data := <-received:
			if !bytes.Equal(msg, data) {
				t.Errorf("received data mismatch: expected %s, got %s", msg, data)
			}
			return
		case <-time.After(100 * time.Millisecond):
			continue
		case <-deadline:
			t.Errorf("timed out waiting for echo from Python")
			return
		}
	}
}

func TestInterfaceErrorPolicyUDPReadLoop(t *testing.T) {
	testutils.SkipShortIntegration(t)

	panicCh := make(chan string, 1)
	restoreHook := setInterfacePanicHookForTest(func(msg string) {
		select {
		case panicCh <- msg:
		default:
		}
	})
	defer restoreHook()

	SetPanicOnInterfaceErrorEnabled(true)
	defer SetPanicOnInterfaceErrorEnabled(false)

	listenPort, forwardPort := allocateUDPPortPair(t)
	iface := mustTestNewUDPInterface(t, "policy_udp", "127.0.0.1", listenPort, "127.0.0.1", forwardPort, nil)
	t.Cleanup(func() {
		if err := iface.Detach(); err != nil && !strings.Contains(err.Error(), "closed network connection") {
			t.Logf("failed to detach UDP interface: %v", err)
		}
	})

	iface.mu.Lock()
	conn := iface.conn
	iface.mu.Unlock()
	if conn == nil {
		t.Fatal("expected UDP interface connection")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("failed to close UDP socket: %v", err)
	}

	select {
	case msg := <-panicCh:
		if !strings.Contains(msg, "udp interface") {
			t.Fatalf("unexpected panic hook message: %q", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for UDP read-loop policy hook")
	}
}

const pythonTCPEchoScript = `
import RNS.Interfaces.TCPInterface as TCPInterface
import time
import sys
import os
import RNS

class Owner:
    def inbound(self, data, interface):
        # Echo back
        interface.process_outgoing(data)

# Mock RNS.log to avoid errors
RNS.log = lambda msg, level=None: None
RNS.Reticulum.MTU = 500
RNS.Reticulum.HEADER_MINSIZE = 2
RNS.LOG_DEBUG = 1
RNS.LOG_INFO = 2
RNS.LOG_WARNING = 3
RNS.LOG_ERROR = 4
RNS.LOG_VERBOSE = 5

config = {
    "name": "test_tcp",
    "listen_ip": "127.0.0.1",
    "listen_port": int(sys.argv[1]),
}

owner = Owner()
# TCPServerInterface will listen and spawn TCPClientInterfaces
iface = TCPInterface.TCPServerInterface(owner, config)
iface.ifac_size = 16
iface.ifac_netname = None
iface.ifac_netkey = None
iface.announce_rate_target = None
iface.announce_rate_grace = None
iface.announce_rate_penalty = None

# Keep alive
try:
    while True:
        time.sleep(1)
except KeyboardInterrupt:
    pass
`

func TestTCPInterfaceParity(t *testing.T) {
	testutils.SkipShortIntegration(t)
	pythonPath := getPythonPath()
	tmpDir, cleanup := testutils.TempDir(t, "rns-tcp-parity-*")
	defer cleanup()

	scriptPath := filepath.Join(tmpDir, "tcp_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonTCPEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	pyListenPort := reserveTCPPort(t)

	cmd := exec.Command("python3", scriptPath, fmt.Sprintf("%v", pyListenPort))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start Python TCP echo: %v", err)
	}
	t.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil {
			t.Logf("failed to kill Python TCP echo: %v", err)
		}
	})

	// Wait for Python to start
	time.Sleep(1000 * time.Millisecond)

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	// Go connects to Python (which is a TCPServerInterface)
	// We use HDLC framing (kiss=false) by default in Test
	goIface := mustTestNewTCPClientInterface(t, "go_tcp", "127.0.0.1", pyListenPort, false, handler)
	t.Cleanup(func() {
		if err := goIface.Detach(); err != nil {
			t.Logf("failed to detach Go TCP interface: %v", err)
		}
	})

	// Wait for connection
	time.Sleep(500 * time.Millisecond)
	if !goIface.Status() {
		t.Fatal("Go TCP interface failed to connect")
	}

	msg := []byte("hello from go to python via tcp")
	if err := goIface.Send(msg); err != nil {
		t.Fatalf("failed to send data to Python: %v", err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Errorf("received data mismatch: expected %s, got %s", msg, data)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("timed out waiting for echo from Python")
	}
}

func TestTCPInterfaceParityKISS(t *testing.T) {
	testutils.SkipShortIntegration(t)
	pythonPath := getPythonPath()
	tmpDir, cleanup := testutils.TempDir(t, "rns-tcp-kiss-parity-*")
	defer cleanup()

	const pythonTCPKISSEchoScript = `
import RNS.Interfaces.TCPInterface as TCPInterface
import time
import sys
import os
import RNS

class Owner:
    def inbound(self, data, interface):
        # Echo back
        interface.process_outgoing(data)

# Mock RNS.log to avoid errors
RNS.log = lambda msg, level=None: None
RNS.Reticulum.MTU = 500
RNS.Reticulum.HEADER_MINSIZE = 2
RNS.LOG_DEBUG = 1
RNS.LOG_INFO = 2
RNS.LOG_WARNING = 3
RNS.LOG_ERROR = 4
RNS.LOG_VERBOSE = 5

# We need to mock ConfigObj to return kiss_framing=True
class MockConfig:
    def __getitem__(self, key):
        if key == "name": return "test_tcp_kiss"
        if key == "listen_ip": return "127.0.0.1"
        if key == "listen_port": return int(sys.argv[1])
        if key == "kiss_framing": return "True"
        return None
    def __contains__(self, key):
        return key in ["name", "listen_ip", "listen_port", "kiss_framing"]
    def as_bool(self, key):
        if key == "kiss_framing": return True
        return False
    def as_int(self, key):
        return None

TCPInterface.Interface.get_config_obj = lambda c: MockConfig()

owner = Owner()
# TCPServerInterface will listen and spawn TCPClientInterfaces
iface = TCPInterface.TCPServerInterface(owner, {})
iface.ifac_size = 16
iface.ifac_netname = None
iface.ifac_netkey = None
iface.announce_rate_target = None
iface.announce_rate_grace = None
iface.announce_rate_penalty = None

# Keep alive
try:
    while True:
        time.sleep(1)
except KeyboardInterrupt:
    pass
`

	scriptPath := filepath.Join(tmpDir, "tcp_kiss_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonTCPKISSEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	pyListenPort := reserveTCPPort(t)

	cmd := exec.Command("python3", scriptPath, fmt.Sprintf("%v", pyListenPort))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start Python TCP KISS echo: %v", err)
	}
	t.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil {
			t.Logf("failed to kill Python TCP KISS echo: %v", err)
		}
	})

	// Wait for Python to start
	time.Sleep(1000 * time.Millisecond)

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	// Go connects to Python (which is a TCPServerInterface)
	// We use KISS framing (kiss=true)
	goIface := mustTestNewTCPClientInterface(t, "go_tcp_kiss", "127.0.0.1", pyListenPort, true, handler)
	t.Cleanup(func() {
		if err := goIface.Detach(); err != nil {
			t.Logf("failed to detach Go TCP interface: %v", err)
		}
	})

	// Wait for connection
	time.Sleep(500 * time.Millisecond)
	if !goIface.Status() {
		t.Fatal("Go TCP interface failed to connect")
	}

	msg := []byte("hello from go to python via tcp kiss")
	if err := goIface.Send(msg); err != nil {
		t.Fatalf("failed to send data to Python: %v", err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Errorf("received data mismatch: expected %s, got %s", msg, data)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("timed out waiting for echo from Python")
	}
}

const pythonLocalEchoScript = `
import socket
import sys
import os
import threading

def handle_client(conn):
    try:
        while True:
            data = conn.recv(4096)
            if not data:
                break
            # Simply echo back everything
            conn.sendall(data)
    except Exception as e:
        pass
    finally:
        conn.close()

def main():
    if len(sys.argv) < 2:
        sys.exit(1)

    addr = sys.argv[1]
    if addr.isdigit():
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind(('127.0.0.1', int(addr)))
    else:
        if os.path.exists(addr):
            os.remove(addr)
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.bind(addr)

    s.listen(5)
    print(f"Listening on {addr}", flush=True)

    try:
        while True:
            conn, _ = s.accept()
            t = threading.Thread(target=handle_client, args=(conn,), daemon=True)
            t.start()
    except KeyboardInterrupt:
        pass
    finally:
        s.close()

if __name__ == "__main__":
    main()
`

func TestLocalInterfaceParity(t *testing.T) {
	testutils.SkipShortIntegration(t)
	pythonPath := getPythonPath()
	tmpDir, cleanup := testutils.TempDir(t, "rns-local-parity-*")
	defer cleanup()

	scriptPath := filepath.Join(tmpDir, "local_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonLocalEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	var cmd *exec.Cmd
	var goIface *LocalClientInterface
	var err error
	pyPort := reserveTCPPort(t)
	socketPath := filepath.Join(tmpDir, "rns-test.sock")

	if runtime.GOOS == "linux" {
		cmd = exec.Command("pipx", "run", "--spec", "rns", "python3", scriptPath, socketPath)
	} else {
		cmd = exec.Command("python3", scriptPath, fmt.Sprintf("%v", pyPort))
	}

	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start Python Local echo: %v", err)
	}
	t.Cleanup(func() {
		if err := cmd.Process.Kill(); err != nil {
			t.Logf("failed to kill Python Local echo: %v", err)
		}
		if err := cmd.Wait(); err != nil {
			t.Logf("Python Local echo wait error: %v", err)
		}
	})

	// Wait for Python to start and create the socket
	deadline := time.Now().Add(5 * time.Second)
	if runtime.GOOS == "linux" {
		for {
			if _, err := os.Stat(socketPath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Logf("Warning: socket file %v not found after 5s", socketPath)
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	} else {
		time.Sleep(1500 * time.Millisecond)
	}

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	// Go connects to Python (which is a LocalServerInterface)
	if runtime.GOOS == "linux" {
		goIface, err = NewLocalClientInterface("go_local", socketPath, 0, handler)
	} else {
		goIface, err = NewLocalClientInterface("go_local", "", pyPort, handler)
	}

	if err != nil {
		t.Fatalf("failed to create Go Local interface: %v", err)
	}
	t.Cleanup(func() {
		if err := goIface.Detach(); err != nil {
			t.Logf("failed to detach Go Local interface: %v", err)
		}
	})

	// Wait for connection with retries
	connected := false
	for range 20 {
		if goIface.Status() {
			connected = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if !connected {
		t.Fatal("Go Local interface failed to connect after multiple attempts")
	}

	msg := []byte("hello from go to python via local interface")
	if err := goIface.Send(msg); err != nil {
		t.Fatalf("failed to send data to Python: %v", err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Errorf("received data mismatch: expected %s, got %s", msg, data)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("timed out waiting for echo from Python")
	}
}

const pythonSerialEchoScript = `
import RNS.Interfaces.SerialInterface as SerialInterface
import time
import sys
import os
import RNS

class Owner:
    def inbound(self, data, interface):
        # Echo back
        interface.process_outgoing(data)

# Mock RNS.log to avoid errors
RNS.log = lambda msg, level=None: None
RNS.Reticulum.MTU = 500
RNS.Reticulum.HEADER_MINSIZE = 2

config = {
    "name": "test_serial",
    "port": sys.argv[1],
    "speed": 115200,
    "databits": 8,
    "parity": "N",
    "stopbits": 1
}

owner = Owner()
iface = SerialInterface.SerialInterface(owner, config)

# Keep alive
try:
    while True:
        time.sleep(1)
except KeyboardInterrupt:
    pass
`

func TestSerialInterfaceParity(t *testing.T) {
	testutils.SkipShortIntegration(t)
	if _, err := exec.LookPath("socat"); err != nil {
		t.Skip("skipping integration test: socat not installed")
	}
	requirePythonModule(t, "serial")
	pythonPath := getPythonPath()
	tmpDir, cleanup := testutils.TempDir(t, "rns-serial-parity-")
	defer cleanup()

	scriptPath := filepath.Join(tmpDir, "serial_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonSerialEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	vserial0 := filepath.Join(tmpDir, "vserial0")
	vserial1 := filepath.Join(tmpDir, "vserial1")
	socatCmd := exec.Command("socat", "-d", "-d",
		fmt.Sprintf("PTY,link=%s,raw,echo=0", vserial0),
		fmt.Sprintf("PTY,link=%s,raw,echo=0", vserial1))

	socatOut := &bytes.Buffer{}
	socatCmd.Stderr = socatOut

	if err := socatCmd.Start(); err != nil {
		t.Fatalf("failed to start socat: %v", err)
	}
	t.Cleanup(func() {
		if err := socatCmd.Process.Kill(); err != nil {
			t.Logf("failed to kill socat: %v", err)
		}
		if err := socatCmd.Wait(); err != nil {
			t.Logf("socat wait error: %v", err)
		}
		fmt.Printf("socat Output: %s\n", socatOut.String())
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err0 := os.Stat(vserial0); err0 == nil {
			if _, err1 := os.Stat(vserial1); err1 == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for socat to create PTY links")
		}
		time.Sleep(100 * time.Millisecond)
	}

	pyCmd := exec.Command("python3", scriptPath, vserial0)
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)
	pyOut := &bytes.Buffer{}
	pyCmd.Stdout = pyOut
	pyCmd.Stderr = pyOut

	if err := pyCmd.Start(); err != nil {
		t.Fatalf("failed to start Python Serial echo: %v", err)
	}
	t.Cleanup(func() {
		if err := pyCmd.Process.Kill(); err != nil {
			t.Logf("failed to kill Python Serial echo: %v", err)
		}
		if err := pyCmd.Wait(); err != nil {
			t.Logf("Python Serial echo wait error: %v", err)
		}
		fmt.Printf("Python Output: %s\n", pyOut.String())
	})

	time.Sleep(2000 * time.Millisecond)

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	goIface, err := NewSerialInterface("go_serial", vserial1, 115200, 8, 1, "N", handler)
	if err != nil {
		t.Fatalf("failed to create Go Serial interface: %v", err)
	}
	t.Cleanup(func() {
		if err := goIface.Detach(); err != nil {
			t.Logf("failed to detach Go Serial interface: %v", err)
		}
	})

	msg := []byte("hello from go to python via serial")
	deadline = time.Now().Add(10 * time.Second)
	for {
		if err := goIface.Send(msg); err != nil {
			t.Fatalf("failed to send data to Python: %v", err)
		}

		select {
		case data := <-received:
			if !bytes.Equal(msg, data) {
				t.Errorf("received data mismatch: expected %s, got %s", msg, data)
			}
			return
		case <-time.After(2 * time.Second):
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for echo from Python (interface status=%v)", goIface.Status())
			}
		}
	}
}

const pythonPipeEchoScript = `
import sys
import time

# Simple HDLC echo script
# Read from stdin, find HDLC frames, echo back to stdout

FLAG = 0x7E
ESC  = 0x7D
ESC_MASK = 0x20

def unescape(data):
    out = bytearray()
    escape = False
    for b in data:
        if escape:
            out.append(b ^ ESC_MASK)
            escape = False
        elif b == ESC:
            escape = True
        else:
            out.append(b)
    return out

def escape(data):
    out = bytearray()
    for b in data:
        if b == ESC:
            out.extend([ESC, ESC ^ ESC_MASK])
        elif b == FLAG:
            out.extend([ESC, FLAG ^ ESC_MASK])
        else:
            out.append(b)
    return out

buffer = bytearray()
while True:
    chunk = sys.stdin.buffer.read(1)
    if not chunk:
        break
    buffer.extend(chunk)

    while FLAG in buffer:
        start = buffer.find(FLAG)
        end = buffer.find(FLAG, start + 1)
        if end != -1:
            frame = buffer[start+1:end]
            payload = unescape(frame)
            # Echo back
            sys.stdout.buffer.write(bytes([FLAG]) + escape(payload) + bytes([FLAG]))
            sys.stdout.buffer.flush()
            buffer = buffer[end+1:]
        else:
            if start > 0:
                buffer = buffer[start:]
            break
`

func TestPipeInterfaceParity(t *testing.T) {
	testutils.SkipShortIntegration(t)
	tmpDir, cleanup := testutils.TempDir(t, "rns-pipe-parity-*")
	defer cleanup()

	scriptPath := filepath.Join(tmpDir, "pipe_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonPipeEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	// Go PipeSubprocessInterface runs the Python echo script
	command := "python3 " + scriptPath
	goIface, err := NewPipeSubprocessInterface("go_pipe", command, 1*time.Second, handler)
	if err != nil {
		t.Fatalf("failed to create Go Pipe interface: %v", err)
	}
	t.Cleanup(func() {
		if err := goIface.Detach(); err != nil {
			t.Logf("failed to detach Go Pipe interface: %v", err)
		}
	})

	// Wait for Python to start
	time.Sleep(500 * time.Millisecond)

	msg := []byte("hello from go to python via pipe")
	if err := goIface.Send(msg); err != nil {
		t.Fatalf("failed to send data to Python: %v", err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Errorf("received data mismatch: expected %s, got %s", msg, data)
		}
	case <-time.After(5 * time.Second):
		t.Errorf("timed out waiting for echo from Python")
	}
}
