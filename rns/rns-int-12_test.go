// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// tunnelSynthesizerPy is a Python transport that synthesizes a tunnel into a
// peer over UDP, mirroring Python Transport.synthesize_tunnel
// (Transport.py:2120-2138). It boots a transport-enabled Reticulum (so
// RNS.Transport.identity exists and can sign the establishment packet),
// locates its UDP interface, calls RNS.Transport.synthesize_tunnel(iface),
// and prints the derived tunnel_id (= FullHash(public_key+interface_hash)),
// public key, and interface hash so the Go receiver can assert parity.
const tunnelSynthesizerPy = `import RNS
import sys
import time
import os

def start_synthesizer(config_dir, listen_port, forward_port):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = True
share_instance = No

[interfaces]
  [[UDP Interface]]
	type = UDPInterface
	enabled = True
	listen_ip = 127.0.0.1
	listen_port = {listen_port}
	forward_ip = 127.0.0.1
	forward_port = {forward_port}
"""
	with open(os.path.join(config_dir, "config"), "w") as f:
		f.write(config_content)

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	# Let the transport and interface come up.
	time.sleep(1)

	if RNS.Transport.identity is None:
		print("Transport identity is None; enable_transport must be True")
		sys.exit(1)

	iface = None
	for i in RNS.Transport.interfaces:
		if i.__class__.__name__ == "UDPInterface":
			iface = i
			break
	if iface is None:
		print("No UDP interface found")
		sys.exit(1)

	public_key = RNS.Transport.identity.get_public_key()
	interface_hash = iface.get_hash()
	tunnel_id = RNS.Identity.full_hash(public_key + interface_hash)
	print(f"TunnelID {tunnel_id.hex()}")
	print(f"PublicKey {public_key.hex()}")
	print(f"InterfaceHash {interface_hash.hex()}")
	sys.stdout.flush()

	# Synthesize the tunnel a few times to be robust against a single
	# dropped UDP packet on the loopback link.
	for n in range(5):
		RNS.Transport.synthesize_tunnel(iface)
		time.sleep(0.5)

	print("Synthesized")
	sys.stdout.flush()

	# Keep the process alive briefly so the outbound packets are flushed.
	time.sleep(2)
	print("Done")
	sys.stdout.flush()

if __name__ == "__main__":
	if len(sys.argv) != 4:
		print("Usage: tunnel_synthesizer.py <config_dir> <listen_port> <forward_port>")
		sys.exit(1)
	start_synthesizer(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]))
`

// TestIntegratedTunnelSynthesizePythonToGo verifies the inbound tunnel
// synthesis wire path: a Python transport synthesizes a tunnel into
// a Go node over UDP RNS; the Go side's tunnel_synthesize_destination receives
// the establishment packet, validates the signature, derives the tunnel_id,
// and creates a tunnel entry whose ID matches what the Python synthesizer
// computed (FullHash(public_key+interface_hash)). This is the parity gate
// against Python Transport.tunnel_synthesize_handler (Transport.py:2141-2158).
func TestIntegratedTunnelSynthesizePythonToGo(t *testing.T) {
	testutils.SkipShortIntegration(t)

	tmpDir := testutils.TempDir(t, "go-reticulum-tunnel-synth-*")

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	scriptPath := filepath.Join(tmpDir, "tunnel_synthesizer.py")
	if err := os.WriteFile(scriptPath, []byte(tunnelSynthesizerPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_synthesizer")

	// Go node: a non-transport Reticulum with a UDP interface peered to the
	// Python node. The tunnel-synthesis destination is registered in Start
	// regardless of transport mode, and local delivery to it happens before
	// the transport-enabled forwarding check.
	goConfigDir := filepath.Join(tmpDir, "go_rns")
	if err := os.MkdirAll(goConfigDir, 0o700); err != nil {
		t.Fatalf("failed to MkdirAll %v: %v", goConfigDir, err)
	}
	goConfigContent := fmt.Sprintf(`[reticulum]
instance_name = %v
enable_transport = False
share_instance = No

[interfaces]
  [[UDP Interface]]
    type = UDPInterface
    enabled = True
    listen_ip = 127.0.0.1
    listen_port = %v
    forward_ip = 127.0.0.1
    forward_port = %v
`, t.Name(), goListenPort, pyListenPort)
	if err := os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0o600); err != nil {
		t.Fatalf("failed to WriteFile config: %v", err)
	}

	logger := mustTestLogger(t, LogDebug)
	ts := NewTransportSystem(logger)
	r := mustTestNewReticulumWithLogger(t, ts, goConfigDir, logger)
	defer closeReticulum(t, r)

	// Start must have registered the tunnel-synthesis destination.
	if ts.tunnelSynthesizeDestination == nil {
		t.Fatal("tunnel synthesize destination was not registered in Start")
	}

	// Start the Python synthesizer.
	pyCmd := exec.Command("python3", scriptPath,
		pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath(t))
	pyStdout, err := pyCmd.StdoutPipe()
	mustTest(t, err)
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pyCmd.Process.Kill()
	})

	// Parse the tunnel_id the Python synthesizer computed.
	tunnelIDHex := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(pyStdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[Python TunnelSynthesizer] %v\n", line)
			if strings.HasPrefix(line, "TunnelID ") {
				select {
				case tunnelIDHex <- strings.TrimPrefix(line, "TunnelID "):
				default:
				}
			}
		}
	}()

	select {
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for Python to report TunnelID")
	case idHex := <-tunnelIDHex:
		if idHex == "" {
			t.Fatal("Python reported empty TunnelID")
		}
		wantTunnelID, err := hex.DecodeString(idHex)
		if err != nil {
			t.Fatalf("invalid tunnel id hex %q: %v", idHex, err)
		}

		// Wait for the Go side to register the tunnel (the Python
		// synthesizer retries, so poll until it lands or timeout).
		deadline := time.Now().Add(30 * time.Second)
		var got *Tunnel
		for time.Now().Before(deadline) {
			ts.mu.Lock()
			entry, ok := ts.tunnels[string(wantTunnelID)]
			ts.mu.Unlock()
			if ok {
				got = entry
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if got == nil {
			ts.mu.Lock()
			n := len(ts.tunnels)
			ts.mu.Unlock()
			t.Fatalf("tunnel %x not registered on Go side (tunnels=%d)", wantTunnelID, n)
		}
		if len(got.ID) != len(wantTunnelID) {
			t.Fatalf("tunnel ID len=%d, want %d", len(got.ID), len(wantTunnelID))
		}
		if !bytesEqual(got.ID, wantTunnelID) {
			t.Fatalf("tunnel ID=%x, want %x", got.ID, wantTunnelID)
		}
		if got.Interface == nil {
			t.Fatal("tunnel entry has nil interface; want the Go receiving interface")
		}
		if got.Paths == nil {
			t.Fatal("tunnel Paths map is nil")
		}
		t.Logf("Go node accepted tunnel %x via interface %v", got.ID, got.Interface.Name())
	}
}
