// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux
// +build integration,linux

package interfaces

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

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
	pythonPath := getPythonPath()
	tmpDir, err := os.MkdirTemp("", "rns-serial-parity-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "serial_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonSerialEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	// Use socat to create a PTY pair
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
	defer func() {
		socatCmd.Process.Kill()
		socatCmd.Wait()
		fmt.Printf("socat Output: %s\n", socatOut.String())
	}()

	// Wait for socat to create the links
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

	// Start Python echo on vserial0
	pyCmd := exec.Command("pipx", "run", "--spec", "rns", "python3", scriptPath, vserial0)
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)
	pyOut := &bytes.Buffer{}
	pyCmd.Stdout = pyOut
	pyCmd.Stderr = pyOut

	if err := pyCmd.Start(); err != nil {
		t.Fatalf("failed to start Python Serial echo: %v", err)
	}
	defer func() {
		pyCmd.Process.Kill()
		pyCmd.Wait()
		fmt.Printf("Python Output: %s\n", pyOut.String())
	}()

	// Wait for Python to start and open the port
	time.Sleep(2000 * time.Millisecond)

	received := make(chan []byte, 1)
	handler := func(data []byte, iface Interface) {
		received <- data
	}

	// Go connects to vserial1
	goIface, err := NewSerialInterface("go_serial", vserial1, 115200, 8, 1, "N", handler)
	if err != nil {
		t.Fatalf("failed to create Go Serial interface: %v", err)
	}
	defer goIface.Detach()

	msg := []byte("hello from go to python via serial")

	// Retry sending a few times to handle cases where the Python
	// serial echo process took longer to start under heavy system load.
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
