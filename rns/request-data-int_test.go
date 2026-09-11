// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// integratedFormInitiatorPy is the Python RNS source of truth for request-data
// pass-through: it sends a link request whose data is a dict, mirroring the
// gonomadnet browser's Micron form-field submission and Python's own
// Link.handle_request, which assigns `request_data = unpacked_request[2]`
// without constraining its MessagePack type (Link.py:803-808).
const integratedFormInitiatorPy = `import RNS
import sys
import time
import os

def start_initiator(dest_hash_hex, pub_key_hex, config_dir, listen_port, forward_port):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = False
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

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_INFO)
	RNS.logdest = RNS.LOG_STDOUT

	dest_hash = bytes.fromhex(dest_hash_hex)
	pub_key = bytes.fromhex(pub_key_hex)

	print(f"Waiting for path to {dest_hash_hex}...")
	sys.stdout.flush()
	timeout = time.time() + 10
	while not RNS.Transport.has_path(dest_hash) and time.time() < timeout:
		time.sleep(0.5)

	if not RNS.Transport.has_path(dest_hash):
		print("Timed out waiting for path")
		sys.exit(1)

	remote_identity = RNS.Identity(create_keys=False)
	remote_identity.load_public_key(pub_key)
	destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, "integrated_test", "formparity")

	if destination.hash != dest_hash:
		print(f"Destination hash mismatch! Expected {dest_hash_hex}, got {destination.hash.hex()}")
		sys.exit(1)

	print("Establishing link...")
	sys.stdout.flush()

	link_established = [False]
	def established(l):
		print(f"Link Established: {l.hash.hex()}")
		sys.stdout.flush()
		link_established[0] = True

	link = RNS.Link(destination, established_callback=established)

	timeout = time.time() + 10
	while not link_established[0] and time.time() < timeout:
		time.sleep(0.5)

	if not link_established[0]:
		print("Timed out waiting for link establishment")
		sys.exit(1)

	print("Sending dict-typed request...")
	sys.stdout.flush()

	response_received = [None]
	def response_callback(r):
		response_received[0] = r.response if hasattr(r, "response") else r

	link.request("form_path", {"var_n": "5", "field_title": "hi", "ignored": 123}, response_callback)

	timeout = time.time() + 10
	while response_received[0] is None and time.time() < timeout:
		time.sleep(0.5)

	if response_received[0] is None:
		print("Timed out waiting for response")
		sys.exit(1)

	if response_received[0] != b"accepted":
		print(f"Unexpected response: {response_received[0]!r}")
		sys.exit(1)

	print("Initiator exiting")
	sys.stdout.flush()

if __name__ == "__main__":
    if len(sys.argv) != 6:
        print("Usage: integrated_form_initiator.py <dest_hash_hex> <pub_key_hex> <config_dir> <listen_port> <forward_port>")
        sys.exit(1)
    start_initiator(sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5]))
`

// TestIntegratedFormDataPythonToGo pins Python↔Go parity for dict-typed request
// data: the Python source of truth sends link.request(path, {..}) and the Go
// handler must observe the decoded map, which the dispatcher previously
// rejected as a malformed request.
func TestIntegratedFormDataPythonToGo(t *testing.T) {
	testutils.SkipShortIntegration(t)
	testutils.SkipIfNoPythonRNS(t)

	tmpDir := testutils.TempDir(t, "go-reticulum-form-parity-*")

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	scriptPath := filepath.Join(tmpDir, "integrated_form_initiator.py")
	if err := os.WriteFile(scriptPath, []byte(integratedFormInitiatorPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_form_initiator")

	goConfigDir := filepath.Join(tmpDir, "go_rns")
	if err := os.MkdirAll(goConfigDir, 0o700); err != nil {
		t.Fatalf("failed to MkdirAll %v: %v", goConfigDir, err)
	}
	goConfigContent := mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)
	if err := os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(goConfigContent), 0o600); err != nil {
		t.Fatalf("failed to WriteFile config: %v", err)
	}

	logger := mustTestLogger(t, LogDebug)
	ts := NewTransportSystem(logger)
	r := mustTestNewReticulumWithLogger(t, ts, goConfigDir, logger)
	defer closeReticulum(t, r)

	id := mustTestNewIdentity(t, true)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "integrated_test", "formparity")

	linkEstablished := make(chan *Link, 1)
	dest.SetLinkEstablishedCallback(func(l *Link) {
		linkEstablished <- l
	})

	received := make(chan map[string]string, 1)
	dest.RegisterRequestHandler("form_path", func(path string, data any, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		fields := map[string]string{}
		if decoded, ok := data.(map[any]any); ok {
			keys := make([]string, 0, len(decoded))
			for key := range decoded {
				keys = append(keys, fmt.Sprintf("%v", key))
			}
			sort.Strings(keys)
			for _, key := range keys {
				fields[key] = fmt.Sprintf("%v", decoded[key])
			}
		}
		received <- fields
		return []byte("accepted")
	}, AllowAll, nil, false)

	go func() {
		for {
			if err := dest.Announce(nil); err != nil {
				logger.Error("failed to announce: %v", err)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()

	pyCmd := exec.Command("python3", scriptPath, fmt.Sprintf("%x", dest.Hash), fmt.Sprintf("%x", id.GetPublicKey()), pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath(t))
	pyStdout, err := pyCmd.StdoutPipe()
	mustTest(t, err)
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := pyCmd.Process.Kill(); err != nil {
			t.Logf("failed to kill pyCmd: %v", err)
		}
	})

	go func() {
		scanner := bufio.NewScanner(pyStdout)
		for scanner.Scan() {
			fmt.Printf("[Python Form Initiator] %v\n", scanner.Text())
		}
	}()

	select {
	case <-linkEstablished:
	case <-time.After(15 * time.Second):
		t.Fatal("Timed out waiting for link establishment from Python")
	}

	select {
	case fields := <-received:
		want := map[string]string{"var_n": "5", "field_title": "hi", "ignored": "123"}
		if len(fields) != len(want) {
			t.Fatalf("request data = %v, want %v", fields, want)
		}
		for key, value := range want {
			if fields[key] != value {
				t.Errorf("request_data[%v] = %v, want %v", key, fields[key], value)
			}
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Timed out waiting for the dict-typed request from Python")
	}

	if err := pyCmd.Wait(); err != nil {
		t.Fatalf("Python form initiator failed: %v", err)
	}
}
