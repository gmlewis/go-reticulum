// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package interfaces

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
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

// pythonMockReticulumSetup stubs RNS.Reticulum.get_instance() for the
// parity echo scripts. Upstream Interface.__init__ now resolves the
// ingress/egress-control defaults via RNS.Reticulum.get_instance()._default_*(),
// which requires a running RNS.Reticulum instance. Constructing a real one
// would spawn transport/shared-instance threads and sockets that interfere
// with these byte-level echo tests, so we install a lightweight mock whose
// _default_* methods return the Interface class constants — exactly what an
// unconfigured real Reticulum instance returns (self.__<field> or
// Interface.<CONSTANT>, with the private fields falsy). get_instance() is only
// called from Interface.__init__ (verified upstream), so the mock only needs
// these methods. Each echo script embeds this after its RNS imports.
const pythonMockReticulumSetup = `
class _MockReticulum:
    _I = RNS.Interfaces.Interface.Interface
    def _default_ic_max_held_announces(self): return self._I.MAX_HELD_ANNOUNCES
    def _default_ic_burst_hold(self): return self._I.IC_BURST_HOLD
    def _default_ic_burst_freq_new(self): return self._I.IC_BURST_FREQ_NEW
    def _default_ic_burst_freq(self): return self._I.IC_BURST_FREQ
    def _default_ic_pr_burst_freq_new(self): return self._I.IC_PR_BURST_FREQ_NEW
    def _default_ic_pr_burst_freq(self): return self._I.IC_PR_BURST_FREQ
    def _default_ec_pr_freq(self): return self._I.EC_PR_FREQ
    def _default_egress_control(self): return self._I.EGRESS_CONTROL
    def _default_ic_new_time(self): return self._I.IC_NEW_TIME
    def _default_ic_burst_penalty(self): return self._I.IC_BURST_PENALTY
    def _default_ic_held_release_interval(self): return self._I.IC_HELD_RELEASE_INTERVAL
RNS.Reticulum.get_instance = staticmethod(lambda: _MockReticulum())
`

func TestAutoInterfaceDiscoveryPacketParity(t *testing.T) {
	testutils.SkipShortIntegration(t)
	pythonPath := getPythonPath(t)
	tmpDir := testutils.TempDir(t, "rns-auto-parity-*")

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

func getPythonPath(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR"); path != "" {
		return path
	}
	t.Fatal("missing required environment variable: ORIGINAL_RETICULUM_REPO_DIR")
	return ""
}

func requirePythonModule(t *testing.T, module string) {
	t.Helper()
	cmd := exec.Command("python3", "-c", "import importlib.util,sys; sys.exit(0 if importlib.util.find_spec(sys.argv[1]) else 1)", module)
	if err := cmd.Run(); err != nil {
		t.Skipf("skipping integration test: python module %q is not available", module)
	}
}

// pythonEchoBindTimeout bounds how long one start attempt waits for the Python
// echo to report that it is listening.
const pythonEchoBindTimeout = 5 * time.Second

// pythonListeningMarker is printed by each Python echo script as soon as it is
// listening; every script must print this same literal. Python raises when its
// bind fails (ThreadingTCPServer for the RNS-based echoes, socket.bind for the
// Local echo), so the marker proves the port is really bound by Python, and a
// Python process that exits before printing it proves the reserved port was
// lost to a parallel test instead.
const pythonListeningMarker = "RNS-ECHO-LISTENING"

// startPythonEchoOnReservedPort starts python3 scriptPath with a loopback TCP
// port as its only argument and returns that port once the script reports that
// it is listening.
//
// A Go bind site adopts a reserved port as a held listener (see reserveTCPPort
// and pending-listener.go), but Python cannot: it creates its own socket, so
// the reservation must be given up before Python binds, and a parallel test's
// reservation or dial can claim the port in that window. Python then fails to
// bind and exits without printing its listening marker, so the whole start is
// retried on a fresh port rather than failing the test.
func startPythonEchoOnReservedPort(t *testing.T, scriptPath, pythonPath, label string) int {
	t.Helper()
	for attempt := 1; attempt <= portReserveAttempts; attempt++ {
		port := reserveUnboundTCPPort(t)
		if tryStartPythonEcho(t, scriptPath, pythonPath, label, fmt.Sprintf("%v", port)) {
			return port
		}
		t.Logf("%v: port %v was claimed before Python bound it; retrying on a fresh port (attempt %v of %v)",
			label, port, attempt, portReserveAttempts)
	}
	t.Fatalf("%v never reported listening on a reserved port after %v attempts", label, portReserveAttempts)
	return 0
}

// startPythonEchoOnReservedUDPPort starts python3 scriptPath with a loopback UDP
// port it must bind as its first argument and goPort as its second, and returns
// the UDP port once the script reports that it is listening. Losing the UDP port
// to a parallel test is retried on a fresh one, exactly as for the TCP echoes
// (see startPythonEchoOnReservedPort); goPort is a port this test holds for a Go
// interface to adopt (see reserveHeldUDPPort), so it stays fixed.
func startPythonEchoOnReservedUDPPort(t *testing.T, scriptPath, pythonPath, label string, goPort int) int {
	t.Helper()
	for attempt := 1; attempt <= portReserveAttempts; attempt++ {
		port := reserveUnboundUDPPort(t)
		if tryStartPythonEcho(t, scriptPath, pythonPath, label, fmt.Sprintf("%v", port), fmt.Sprintf("%v", goPort)) {
			return port
		}
		t.Logf("%v: port %v was claimed before Python bound it; retrying on a fresh port (attempt %v of %v)",
			label, port, attempt, portReserveAttempts)
	}
	t.Fatalf("%v never reported listening on a reserved port after %v attempts", label, portReserveAttempts)
	return 0
}

// tryStartPythonEcho starts python3 scriptPath with args as its arguments and
// reports whether the script reported that it is listening before exiting or
// timing out. A failed start is killed and reaped here; a successful one is
// killed and reaped at test end.
//
// Readiness comes from the script's own listening marker rather than a fixed
// "wait for Python to start" sleep: the tests used to sleep 1s (1.5s for the
// Local echo) whether or not Python was up, and reported a failed start much
// later as a connect timeout.
func tryStartPythonEcho(t *testing.T, scriptPath, pythonPath, label string, args ...string) bool {
	t.Helper()
	cmd := exec.Command("python3", append([]string{scriptPath}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+pythonPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to capture %v stdout: %v", label, err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start %v: %v", label, err)
	}
	proc := &pythonEchoProcess{cmd: cmd, exited: make(chan struct{})}
	go func() {
		defer close(proc.exited)
		_ = cmd.Wait()
	}()
	if !waitForListeningMarker(stdout, proc.exited, pythonEchoBindTimeout) {
		killPythonEcho(t, proc, label)
		return false
	}
	t.Cleanup(func() { killPythonEcho(t, proc, label) })
	return true
}

// pythonEchoProcess is a Python echo script with the goroutine that reaps it.
type pythonEchoProcess struct {
	cmd    *exec.Cmd
	exited chan struct{}
}

// waitForListeningMarker reports whether stdout carries the listening marker
// before the process exits or timeout elapses. The scanner goroutine ends when
// the process is reaped and its pipe closes, so a failed attempt leaves no
// reader behind.
func waitForListeningMarker(stdout io.Reader, exited <-chan struct{}, timeout time.Duration) bool {
	listening := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.Contains(scanner.Text(), pythonListeningMarker) {
				close(listening)
				return
			}
		}
	}()
	select {
	case <-listening:
		return true
	case <-exited:
		return false
	case <-time.After(timeout):
		return false
	}
}

// killPythonEcho stops a Python echo script started by tryStartPythonEcho and
// waits for the goroutine that reaps it.
func killPythonEcho(t *testing.T, proc *pythonEchoProcess, label string) {
	t.Helper()
	if err := proc.cmd.Process.Kill(); err != nil {
		t.Logf("failed to kill %v: %v", label, err)
	}
	select {
	case <-proc.exited:
	case <-time.After(pythonEchoBindTimeout):
		t.Errorf("%v did not exit after being killed", label)
	}
}

const pythonUDPEchoScript = `
import RNS.Interfaces.UDPInterface as UDPInterface
import time
import sys
import os
import RNS
` + pythonMockReticulumSetup + `
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
print("RNS-ECHO-LISTENING", flush=True)

# Keep alive
try:
    while True:
        time.sleep(1)
except KeyboardInterrupt:
    pass
`

func TestUDPInterfaceParity(t *testing.T) {
	testutils.SkipShortIntegration(t)
	pythonPath := getPythonPath(t)
	tmpDir := testutils.TempDir(t, "rns-udp-parity-*")

	scriptPath := filepath.Join(tmpDir, "udp_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonUDPEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	// Python binds pyListenPort itself, so that one is only reserved; the Go
	// interface adopts goListenPort from the socket held here.
	goListenPort := reserveHeldUDPPort(t)
	pyListenPort := startPythonEchoOnReservedUDPPort(t, scriptPath, pythonPath, "Python UDP echo", goListenPort)

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
	listenPort, forwardPort := allocateUDPPortPair(t)
	iface := mustTestNewUDPInterface(t, "policy_udp", "127.0.0.1", listenPort, "127.0.0.1", forwardPort, nil)
	restoreHook := iface.setInterfacePanicHookForTest(func(msg string) {
		select {
		case panicCh <- msg:
		default:
		}
	})
	defer restoreHook()
	iface.SetPanicOnInterfaceErrorEnabled(true)
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
` + pythonMockReticulumSetup + `
config = {
    "name": "test_tcp",
    "listen_ip": "127.0.0.1",
    "listen_port": int(sys.argv[1]),
}

owner = Owner()
# TCPServerInterface will listen and spawn TCPClientInterfaces
iface = TCPInterface.TCPServerInterface(owner, config)
print("RNS-ECHO-LISTENING", flush=True)
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
	pythonPath := getPythonPath(t)
	tmpDir := testutils.TempDir(t, "rns-tcp-parity-*")

	scriptPath := filepath.Join(tmpDir, "tcp_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonTCPEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	pyListenPort := startPythonEchoOnReservedPort(t, scriptPath, pythonPath, "Python TCP echo")

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
	waitForIfaceRunning(t, goIface, 3*time.Second)

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
	pythonPath := getPythonPath(t)
	tmpDir := testutils.TempDir(t, "rns-tcp-kiss-parity-*")

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
` + pythonMockReticulumSetup + `
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
print("RNS-ECHO-LISTENING", flush=True)
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

	pyListenPort := startPythonEchoOnReservedPort(t, scriptPath, pythonPath, "Python TCP KISS echo")

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
	waitForIfaceRunning(t, goIface, 3*time.Second)

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
    print(f"RNS-ECHO-LISTENING {addr}", flush=True)

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
	pythonPath := getPythonPath(t)
	tmpDir := testutils.TempDir(t, "rns-local-parity-*")

	scriptPath := filepath.Join(tmpDir, "local_echo.py")
	if err := os.WriteFile(scriptPath, []byte(pythonLocalEchoScript), 0o644); err != nil {
		t.Fatal(err)
	}

	var goIface *LocalClientInterface
	var err error
	var pyPort int
	socketPath := filepath.Join(tmpDir, "rns-test.sock")

	// Linux hands Python a unix socket path; elsewhere the rendezvous is a TCP
	// port Python binds itself, which the test suite cannot hold for it (see
	// startPythonEchoOnReservedPort). Both branches wait for the script's own
	// listening marker instead of a fixed sleep.
	if runtime.GOOS == "linux" {
		if !tryStartPythonEcho(t, scriptPath, pythonPath, "Python Local echo", socketPath) {
			t.Fatalf("Python Local echo never reported listening on %v", socketPath)
		}
	} else {
		pyPort = startPythonEchoOnReservedPort(t, scriptPath, pythonPath, "Python Local echo")
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

	// Wait for connection
	waitForIfaceRunning(t, goIface, 5*time.Second)

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
` + pythonMockReticulumSetup + `
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
	pythonPath := getPythonPath(t)
	tmpDir := testutils.TempDir(t, "rns-serial-parity-")

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
	tmpDir := testutils.TempDir(t, "rns-pipe-parity-*")

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
