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

	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// blackholeListRequesterPy is a Python client that fetches the /list RPC
// from a Go node's rnstransport.info.blackhole destination and prints the
// received list re-packed as msgpack hex. It mirrors the fetch half of
// Discovery.BlackholeUpdater (Discovery.py:683-712): derive the destination
// hash from the source identity hash, await the path, construct the OUT
// destination, establish a link, request "/list", and emit the response.
const blackholeListRequesterPy = `import RNS
import sys
import time
import os
from RNS.vendor import umsgpack

def start_requester(identity_hash_hex, pub_key_hex, config_dir, listen_port, forward_port):
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

	reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_DEBUG)
	RNS.logdest = RNS.LOG_STDOUT

	identity_hash = bytes.fromhex(identity_hash_hex)
	pub_key = bytes.fromhex(pub_key_hex)

	destination_hash = RNS.Destination.hash_from_name_and_identity("rnstransport.info.blackhole", identity_hash)
	print(f"Waiting for path to {destination_hash.hex()}...")
	sys.stdout.flush()
	timeout = time.time() + 20
	while not RNS.Transport.has_path(destination_hash) and time.time() < timeout:
		time.sleep(0.5)

	if not RNS.Transport.has_path(destination_hash):
		print("Timed out waiting for path")
		sys.exit(1)

	remote_identity = RNS.Identity(create_keys=False)
	remote_identity.load_public_key(pub_key)
	destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, "rnstransport", "info", "blackhole")

	if destination.hash != destination_hash:
		print(f"Destination hash mismatch! Expected {destination_hash.hex()}, got {destination.hash.hex()}")
		sys.exit(1)

	print("Establishing link...")
	sys.stdout.flush()

	link_established = [False]
	def established(l):
		link_established[0] = True

	link = RNS.Link(destination, established_callback=established)

	timeout = time.time() + 20
	while not link_established[0] and time.time() < timeout:
		time.sleep(0.5)

	if not link_established[0]:
		print("Timed out waiting for link establishment")
		sys.exit(1)

	print("Requesting /list...")
	sys.stdout.flush()

	response_received = [None]
	def response_callback(r):
		if hasattr(r, "response"):
			response_received[0] = r.response

	link.request("/list", None, response_callback)

	# A single RESPONSE packet can be silently lost on localhost under
	# parallel-test UDP load (RNS never retransmits it), so re-issue the
	# request periodically rather than failing on the first silence.
	deadline = time.time() + 90
	while response_received[0] is None and time.time() < deadline:
		time.sleep(5)
		link.request("/list", None, response_callback)

	if response_received[0] is None:
		print("Timed out waiting for response")
		sys.exit(1)

	if not isinstance(response_received[0], dict):
		print(f"Response is not a dict: {type(response_received[0])}")
		sys.exit(1)

	print("ResponseHex " + umsgpack.packb(response_received[0]).hex())
	print("ResponseCount " + str(len(response_received[0])))
	print("Requester done")
	sys.stdout.flush()

if __name__ == "__main__":
	if len(sys.argv) != 6:
		print("Usage: blackhole_list_requester.py <identity_hash_hex> <pub_key_hex> <config_dir> <listen_port> <forward_port>")
		sys.exit(1)
	start_requester(sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5]))
`

// TestIntegratedBlackholeListPythonToGo verifies the /list blackhole RPC
// end-to-end: a Go node with publish_blackhole enabled registers the
// rnstransport.info.blackhole destination, a Python client fetches /list
// over a link, and the response Python receives decodes to the exact
// blackholed_identities structure the Go node serves (captured golden from
// Python umsgpack). This is the wire-parity gate.
func TestIntegratedBlackholeListPythonToGo(t *testing.T) {
	testutils.SkipShortIntegration(t)

	tmpDir := testutils.TempDir(t, "go-reticulum-blackhole-list-*")

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	scriptPath := filepath.Join(tmpDir, "blackhole_list_requester.py")
	if err := os.WriteFile(scriptPath, []byte(blackholeListRequesterPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_requester")

	// Initialize Go Reticulum with publish_blackhole enabled.
	goConfigDir := filepath.Join(tmpDir, "go_rns")
	if err := os.MkdirAll(goConfigDir, 0o700); err != nil {
		t.Fatalf("failed to MkdirAll %v: %v", goConfigDir, err)
	}
	goConfigContent := fmt.Sprintf(`[reticulum]
instance_name = %v
enable_transport = False
share_instance = No
publish_blackhole = True

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

	// Start must have registered the blackhole destination on the transport
	// identity (publish_blackhole was set).
	blackholeDst := ts.blackholeDestination
	if blackholeDst == nil {
		t.Fatal("publish_blackhole enabled but blackholeDestination was not registered")
	}
	transportIdentity := ts.Identity()
	if transportIdentity == nil {
		t.Fatal("transport identity is nil after Start")
	}

	// Seed the in-memory blackhole set with a mix of own- and remote-sourced
	// entries (the /list handler serves ALL entries, not just own-sourced).
	own := transportIdentity.Hash
	src := mustHexDecode(t, "112233445566778899aabbccddeeff00")
	ih1 := mustHexDecode(t, "deadbeefdeadbeefdeadbeefdeadbeef")
	ih2 := mustHexDecode(t, "cafebabecafebabecafebabecafebabe")
	ih3 := mustHexDecode(t, "feedfacefeedfacefeedfacefeedface")
	until2 := time.Unix(9_900_000_000, 0)
	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.blackholedIdentities[string(ih1)] = BlackholeIdentityEntry{IdentityHash: ih1, Source: own, Reason: "local-ih1", Until: nil}
	ts.blackholedIdentities[string(ih2)] = BlackholeIdentityEntry{IdentityHash: ih2, Source: src, Reason: "remote-ih2", Until: &until2}
	ts.blackholedIdentities[string(ih3)] = BlackholeIdentityEntry{IdentityHash: ih3, Source: own, Reason: "local-ih3", Until: nil}
	ts.mu.Unlock()

	// Periodically announce the blackhole destination so Python can find the
	// path (Python mgmt-announce loop analog; the Go port does not run one).
	// The goroutine is stopped before the Reticulum closes so it does not
	// race with teardown (the race detector flags otherwise).
	announceStop := make(chan struct{})
	announceDone := make(chan struct{})
	go func() {
		defer close(announceDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-announceStop:
				return
			case <-ticker.C:
				if err := blackholeDst.Announce(nil); err != nil {
					logger.Error("failed to announce blackhole destination: %v", err)
				}
			}
		}
	}()
	// Stop the announce goroutine and wait for it to exit before the
	// Reticulum closes (registered after closeReticulum, so it runs first).
	defer func() { close(announceStop); <-announceDone }()

	// Start the Python requester.
	pyCmd := exec.Command("python3", scriptPath,
		fmt.Sprintf("%x", transportIdentity.Hash),
		fmt.Sprintf("%x", transportIdentity.GetPublicKey()),
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

	responseHex := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(pyStdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[Python BlackholeRequester] %v\n", line)
			if strings.HasPrefix(line, "ResponseHex ") {
				select {
				case responseHex <- strings.TrimPrefix(line, "ResponseHex "):
				default:
				}
			}
		}
	}()

	var gotHex string
	select {
	case gotHex = <-responseHex:
	case <-time.After(120 * time.Second):
		t.Fatal("Timed out waiting for /list response from Python")
	}

	// The response Python received, re-packed by umsgpack, must decode to the
	// same blackholed_identities structure the Go node serves. The expected
	// map is built from the actual transport identity hash (auto-generated at
	// startup) rather than the unit-test golden hex, since "own" varies per
	// run.
	gotMap, err := msgpack.Unpack(mustHexDecode(t, gotHex))
	if err != nil {
		t.Fatalf("unpacking Python response hex: %v", err)
	}
	got, ok := gotMap.(map[any]any)
	if !ok {
		t.Fatalf("Python response type %T, want map", gotMap)
	}
	want := map[any]any{
		string(ih1): map[any]any{"source": own, "until": nil, "reason": "local-ih1"},
		string(ih2): map[any]any{"source": src, "until": float64(9_900_000_000), "reason": "remote-ih2"},
		string(ih3): map[any]any{"source": own, "until": nil, "reason": "local-ih3"},
	}
	assertBlackholeMapsEqual(t, got, want)

	if err := pyCmd.Wait(); err != nil {
		t.Fatalf("Python blackhole requester failed: %v", err)
	}
}

// blackholePublisherPy is a Python node with publish_blackhole enabled that
// seeds a known blackholed_identities set and announces its
// rnstransport.info.blackhole destination, serving the /list RPC. It prints
// its transport identity hash + public key so the Go fetcher can target it.
const blackholePublisherPy = `import RNS
import sys
import os
import time

def main(config_dir, listen_port, forward_port):
	if not os.path.exists(config_dir):
		os.makedirs(config_dir)

	config_content = f"""
[reticulum]
enable_transport = False
share_instance = No
publish_blackhole = True

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

	# Wait for the transport identity + blackhole destination to exist.
	while RNS.Transport.identity is None:
		time.sleep(0.05)
	while not hasattr(RNS.Transport, "blackhole_destination") or RNS.Transport.blackhole_destination is None:
		time.sleep(0.05)

	own_hash = RNS.Transport.identity.hash
	own_pub = RNS.Transport.identity.get_public_key()
	print("IdentityHash " + own_hash.hex())
	print("IdentityPub " + own_pub.hex())
	sys.stdout.flush()

	# Seed a known blackholed set (source = own transport identity).
	ih1 = bytes.fromhex("deadbeefdeadbeefdeadbeefdeadbeef")
	ih2 = bytes.fromhex("cafebabecafebabecafebabecafebabe")
	RNS.Transport.blackhole_identity(ih1, None, "py-ih1")
	RNS.Transport.blackhole_identity(ih2, 9900000000, "py-ih2")

	print("SeededCount " + str(len(RNS.Transport.blackholed_identities)))
	sys.stdout.flush()

	# Announce the blackhole destination so the Go fetcher can discover a
	# path (the built-in mgmt announce loop waits ~15s; announce faster).
	while True:
		try:
			RNS.Transport.blackhole_destination.announce()
		except Exception as e:
			print("Announce error: " + str(e))
			sys.stdout.flush()
		time.sleep(0.5)

if __name__ == "__main__":
	if len(sys.argv) != 4:
		print("Usage: blackhole_publisher.py <config_dir> <listen_port> <forward_port>")
		sys.exit(1)
	main(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]))
`

// TestIntegratedBlackholeEnablePythonToGo verifies the real BlackholeUpdater
// fetch path end-to-end: a Go node with a configured
// blackhole_source fetches the /list RPC from a Python publisher, merges the
// result into its in-memory blackholed_identities, and persists the fetched
// list to blackholepath/<hex(source)>. The resulting Go in-memory set must
// match what Python published. The acceptance gate is
// `./scripts/test-integration.sh ./rns -run 'EnableBlackhole'`.
func TestIntegratedBlackholeEnablePythonToGo(t *testing.T) {
	testutils.SkipShortIntegration(t)

	tmpDir := testutils.TempDir(t, "go-reticulum-blackhole-updater-*")

	pyListenPort, goListenPort := allocateUDPPortPair(t)

	scriptPath := filepath.Join(tmpDir, "blackhole_publisher.py")
	if err := os.WriteFile(scriptPath, []byte(blackholePublisherPy), 0o644); err != nil {
		t.Fatal(err)
	}
	pyConfigDir := filepath.Join(tmpDir, "py_publisher")

	// Start the Python publisher and capture its transport identity hash.
	pyCmd := exec.Command("python3", scriptPath, pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
	pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath(t))
	pyStdout, err := pyCmd.StdoutPipe()
	mustTest(t, err)
	pyCmd.Stderr = pyCmd.Stdout
	if err := pyCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pyCmd.Process.Kill() })

	pyIdentityHash := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(pyStdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[Python BlackholePublisher] %v\n", line)
			if strings.HasPrefix(line, "IdentityHash ") {
				select {
				case pyIdentityHash <- strings.TrimPrefix(line, "IdentityHash "):
				default:
				}
			}
		}
	}()

	var sourceHex string
	select {
	case sourceHex = <-pyIdentityHash:
	case <-time.After(20 * time.Second):
		t.Fatal("Timed out waiting for Python publisher identity hash")
	}
	sourceIdentityHash := mustHexDecode(t, sourceHex)

	// Initialize the Go Reticulum with a UDP interface to the publisher.
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

	ih1 := mustHexDecode(t, "deadbeefdeadbeefdeadbeefdeadbeef")
	ih2 := mustHexDecode(t, "cafebabecafebabecafebabecafebabe")

	// Construct a BlackholeUpdater with the real fetch, targeting the
	// Python publisher, with shortened intervals so the test runs quickly.
	// (EnableBlackholeUpdater's wiring is covered by the unit tests; this
	// test exercises the real blackholeFetch + merge + persist path.)
	updater := NewBlackholeUpdater(ts, func() [][]byte { return [][]byte{sourceIdentityHash} }, ts.blackholeFetch)
	updater.initialWait = 500 * time.Millisecond
	updater.jobInterval = 500 * time.Millisecond
	updater.updateInterval = 1 * time.Millisecond
	updater.Start()
	defer updater.Stop()

	// Poll for the merged entries (the fetch awaits the path, establishes a
	// link, requests /list, and merges — allow time for the announce to
	// propagate and the link/request round-trip).
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		ts.mu.Lock()
		_, has1 := ts.blackholedIdentities[string(ih1)]
		_, has2 := ts.blackholedIdentities[string(ih2)]
		ts.mu.Unlock()
		if has1 && has2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	ts.mu.Lock()
	e1, has1 := ts.blackholedIdentities[string(ih1)]
	e2, has2 := ts.blackholedIdentities[string(ih2)]
	ts.mu.Unlock()
	if !has1 {
		t.Fatal("ih1 was not merged from the Python /list fetch")
	}
	if !has2 {
		t.Fatal("ih2 was not merged from the Python /list fetch")
	}
	// The publisher set source = its own transport identity for both.
	if !bytesEqual(e1.Source, sourceIdentityHash) || e1.Reason != "py-ih1" || e1.Until != nil {
		t.Fatalf("ih1 merged incorrectly: %+v", e1)
	}
	if !bytesEqual(e2.Source, sourceIdentityHash) || e2.Reason != "py-ih2" || e2.Until == nil || e2.Until.Unix() != 9_900_000_000 {
		t.Fatalf("ih2 merged incorrectly: %+v", e2)
	}

	// The fetched list must be persisted to blackholepath/<hex(source)>.
	if _, err := os.Stat(filepath.Join(ts.blackholePath, hex.EncodeToString(sourceIdentityHash))); err != nil {
		t.Fatalf("persisted source list missing: %v", err)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
