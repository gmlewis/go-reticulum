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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func getPythonPath() string {
	if path := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR"); path != "" {
		return path
	}
	// Fallback for local development if env var is not set but symlink exists
	if _, err := os.Stat("original-reticulum-repo"); err == nil {
		abs, err := filepath.Abs("original-reticulum-repo")
		if err == nil {
			return abs
		}
	}
	log.Fatalf("missing required environment variable: ORIGINAL_RETICULUM_REPO_DIR")
	return ""
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
	pythonPath := getPythonPath()
	tmpDir, err := os.MkdirTemp("", "rns-udp-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "udp_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonUDPEchoScript), 0644); err != nil {
		t.Fatal(err)
	}

	pyListenPort := 37430
	goListenPort := 37431

	cmd := exec.Command("python3", scriptPath, fmt.Sprintf("%v", pyListenPort), fmt.Sprintf("%v", goListenPort))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start Python UDP echo: %v", err)
	}
	defer cmd.Process.Kill()

	// Wait for Python to start
	time.Sleep(500 * time.Millisecond)

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	goIface, err := NewUDPInterface("go_udp", "127.0.0.1", goListenPort, "127.0.0.1", pyListenPort, handler)
	if err != nil {
		t.Fatalf("failed to create Go UDP interface: %v", err)
	}
	defer goIface.Detach()

	msg := []byte("hello from go to python")
	if err := goIface.Send(msg); err != nil {
		t.Fatalf("failed to send data to Python: %v", err)
	}

	select {
	case data := <-received:
		if !bytes.Equal(msg, data) {
			t.Errorf("received data mismatch: expected %s, got %s", msg, data)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("timed out waiting for echo from Python")
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
	pythonPath := getPythonPath()
	tmpDir, err := os.MkdirTemp("", "rns-tcp-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "tcp_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonTCPEchoScript), 0644); err != nil {
		t.Fatal(err)
	}

	pyListenPort := 37432

	cmd := exec.Command("python3", scriptPath, fmt.Sprintf("%v", pyListenPort))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start Python TCP echo: %v", err)
	}
	defer cmd.Process.Kill()

	// Wait for Python to start
	time.Sleep(1000 * time.Millisecond)

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	// Go connects to Python (which is a TCPServerInterface)
	// We use HDLC framing (kiss=false) by default in Test
	goIface, err := NewTCPClientInterface("go_tcp", "127.0.0.1", pyListenPort, false, handler)
	if err != nil {
		t.Fatalf("failed to create Go TCP interface: %v", err)
	}
	defer goIface.Detach()

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
	pythonPath := getPythonPath()
	tmpDir, err := os.MkdirTemp("", "rns-tcp-kiss-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

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
	if err := os.WriteFile(scriptPath, []byte(pythonTCPKISSEchoScript), 0644); err != nil {
		t.Fatal(err)
	}

	pyListenPort := 37434

	cmd := exec.Command("python3", scriptPath, fmt.Sprintf("%v", pyListenPort))
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start Python TCP KISS echo: %v", err)
	}
	defer cmd.Process.Kill()

	// Wait for Python to start
	time.Sleep(1000 * time.Millisecond)

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	// Go connects to Python (which is a TCPServerInterface)
	// We use KISS framing (kiss=true)
	goIface, err := NewTCPClientInterface("go_tcp_kiss", "127.0.0.1", pyListenPort, true, handler)
	if err != nil {
		t.Fatalf("failed to create Go TCP interface: %v", err)
	}
	defer goIface.Detach()

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
	pythonPath := getPythonPath()
	tmpDir, err := os.MkdirTemp("", "rns-local-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "local_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonLocalEchoScript), 0644); err != nil {
		t.Fatal(err)
	}

	var cmd *exec.Cmd
	var goIface *LocalClientInterface
	pyPort := 37436
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
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

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
	defer goIface.Detach()

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
	tmpDir, err := os.MkdirTemp("", "rns-pipe-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "pipe_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonPipeEchoScript), 0644); err != nil {
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
	defer goIface.Detach()

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
