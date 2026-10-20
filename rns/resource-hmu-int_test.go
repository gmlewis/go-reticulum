// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package rns

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// resourceHMUIntegrationPayload is the application payload both directions
// transfer. It is larger than HASHMAP_MAX_LEN parts at the static link MDU, so
// the advertisement necessarily carries only its first hashmap segment and the
// receiver must ask for the rest with RESOURCE_HMU.
const resourceHMUIntegrationPayload = 60000

func resourceHMUIntegrationData(seed byte) []byte {
	data := make([]byte, resourceHMUIntegrationPayload)
	for i := range data {
		data[i] = byte(i*31) + seed
	}
	return data
}

func resourceHMUIntegrationDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// pythonResourceHMUIntegrationScript drives either end of the transfer with the
// installed Python RNS: a responder that validates the request payload and
// answers with a large payload, or an initiator that sends one and validates
// the answer.
const pythonResourceHMUIntegrationScript = `
import hashlib, os, sys, time
import RNS

N = %d

def payload(seed):
    return bytes(((i * 31) + seed) & 0xFF for i in range(N))

def digest(data):
    return hashlib.sha256(data).hexdigest()

def write_config(config_dir, listen_port, forward_port):
    os.makedirs(config_dir, exist_ok=True)
    with open(os.path.join(config_dir, "config"), "w") as f:
        f.write(f"""
[reticulum]
instance_name = resource-hmu-{os.getpid()}
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
""")

def responder(config_dir):
    write_config(config_dir, int(sys.argv[3]), int(sys.argv[4]))
    RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_WARNING)
    identity = RNS.Identity()
    dest = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "integrated_test", "hmu_resource")
    print("Destination Hash: " + dest.hash.hex())
    print("Identity Public Key: " + identity.get_public_key().hex())
    sys.stdout.flush()

    response_payload = payload(7)

    def handler(path, data, request_id, link_id, remote_identity, requested_at):
        print("REQUEST DIGEST: " + digest(data))
        sys.stdout.flush()
        return response_payload

    dest.register_request_handler("hmu_path", response_generator=handler, allow=RNS.Destination.ALLOW_ALL)
    while True:
        dest.announce()
        time.sleep(0.5)

def initiator(config_dir):
    write_config(config_dir, int(sys.argv[3]), int(sys.argv[4]))
    RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_WARNING)
    dest_hash = bytes.fromhex(sys.argv[5])
    pub_key = bytes.fromhex(sys.argv[6])

    remote_identity = RNS.Identity(create_keys=False)
    remote_identity.load_public_key(pub_key)
    destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, "integrated_test", "hmu_resource")
    if destination.hash != dest_hash:
        print("Destination hash mismatch")
        sys.exit(1)

    timeout = time.time() + 15
    while not RNS.Transport.has_path(dest_hash) and time.time() < timeout:
        time.sleep(0.2)
    if not RNS.Transport.has_path(dest_hash):
        print("Timed out waiting for path")
        sys.exit(1)

    established = [False]
    def on_established(link):
        established[0] = True
    link = RNS.Link(destination, established_callback=on_established)
    timeout = time.time() + 15
    while not established[0] and time.time() < timeout:
        time.sleep(0.1)
    if not established[0]:
        print("Timed out waiting for link establishment")
        sys.exit(1)

    request_payload = payload(3)
    response = [None]
    def on_response(receipt):
        response[0] = receipt.response
    def on_failed(receipt):
        print("Request failed")
        sys.stdout.flush()
    print("REQUEST DIGEST: " + digest(request_payload))
    sys.stdout.flush()
    link.request("hmu_path", request_payload, response_callback=on_response, failed_callback=on_failed, timeout=60)
    timeout = time.time() + 90
    while response[0] is None and time.time() < timeout:
        time.sleep(0.1)
    if response[0] is None:
        print("Timed out waiting for the response")
        sys.exit(1)
    print("RESPONSE DIGEST: " + digest(response[0]))
    sys.stdout.flush()

if __name__ == "__main__":
    if sys.argv[1] == "responder":
        responder(sys.argv[2])
    else:
        initiator(sys.argv[2])
`

func writePythonResourceHMUScript(t *testing.T, tmpDir string) string {
	t.Helper()
	script := fmt.Sprintf(pythonResourceHMUIntegrationScript, resourceHMUIntegrationPayload)
	path := filepath.Join(tmpDir, "resource_hmu_peer.py")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func startPythonResourceHMUPeer(t *testing.T, scriptPath string, args ...string) (*exec.Cmd, *bufio.Scanner) {
	t.Helper()
	cmd := exec.Command("python3", append([]string{scriptPath}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath(t))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	return cmd, bufio.NewScanner(stdout)
}

// startGoResourceHMUTransport boots an isolated Go Reticulum over a UDP pair.
func startGoResourceHMUTransport(t *testing.T, tmpDir string, listenPort, forwardPort int) (*TransportSystem, *Reticulum) {
	t.Helper()
	configDir := filepath.Join(tmpDir, "go_rns")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("failed to MkdirAll %v: %v", configDir, err)
	}
	config := mustUDPConfig(t.Name(), listenPort, forwardPort, false)
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte(config), 0o600); err != nil {
		t.Fatalf("failed to WriteFile config: %v", err)
	}
	logger := mustTestLogger(t, LogDebug)
	ts := NewTransportSystem(logger)
	r := mustTestNewReticulumWithLogger(t, ts, configDir, logger)
	t.Cleanup(func() { closeReticulum(t, r) })
	return ts, r
}

// startGoAnnouncer announces dest until the returned stop function is called.
func startGoAnnouncer(t *testing.T, dest *Destination) func() {
	t.Helper()
	stop := make(chan struct{})
	var once sync.Once
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = dest.Announce(nil)
			}
		}
	}()
	return func() {
		once.Do(func() { close(stop) })
		<-done
	}
}

// TestIntegratedResourceHMUGoToPython transfers a payload larger than one
// advertisement hashmap segment from the Go implementation to the installed
// Python RNS reference and back: the >50 KB request exercises the Go sender's
// truncated advertisement and the Python receiver's RESOURCE_HMU request, and
// the >50 KB response exercises the Python sender against the Go receiver's HMU
// handling.
func TestIntegratedResourceHMUGoToPython(t *testing.T) {
	testutils.SkipShortIntegration(t)
	testutils.SkipIfNoPythonRNS(t)

	tmpDir := testutils.TempDir(t, "go-reticulum-resource-hmu-*")
	script := writePythonResourceHMUScript(t, tmpDir)
	pyListenPort, goListenPort := allocateUDPPortPair(t)
	pyConfigDir := filepath.Join(tmpDir, "py_responder")

	_, scanner := startPythonResourceHMUPeer(t, script, "responder", pyConfigDir,
		fmt.Sprintf("%d", pyListenPort), fmt.Sprintf("%d", goListenPort))

	var destHashHex, pubKeyHex string
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Printf("[Python Responder] %v\n", line)
		if strings.HasPrefix(line, "Destination Hash: ") {
			destHashHex = strings.TrimPrefix(line, "Destination Hash: ")
		} else if strings.HasPrefix(line, "Identity Public Key: ") {
			pubKeyHex = strings.TrimPrefix(line, "Identity Public Key: ")
			break
		}
	}
	if destHashHex == "" || pubKeyHex == "" {
		t.Fatal("could not read destination hash / public key from the Python responder")
	}
	destHash, err := HexToBytes(destHashHex)
	if err != nil {
		t.Fatal(err)
	}
	pubKey, err := HexToBytes(pubKeyHex)
	if err != nil {
		t.Fatal(err)
	}

	var pyLines []string
	var pyMu sync.Mutex
	pyDone := make(chan struct{})
	go func() {
		defer close(pyDone)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[Python Responder] %v\n", line)
			pyMu.Lock()
			pyLines = append(pyLines, line)
			pyMu.Unlock()
		}
	}()

	ts, _ := startGoResourceHMUTransport(t, tmpDir, goListenPort, pyListenPort)

	deadline := time.Now().Add(15 * time.Second)
	for !ts.HasPath(destHash) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !ts.HasPath(destHash) {
		t.Fatal("timed out waiting for the Python responder's announce")
	}

	remoteIdentity, err := NewIdentity(false, ts.GetLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := remoteIdentity.LoadPublicKey(pubKey); err != nil {
		t.Fatal(err)
	}
	remoteDest := mustTestNewDestination(t, ts, remoteIdentity, DestinationOut, DestinationSingle, "integrated_test", "hmu_resource")
	if !bytes.Equal(remoteDest.Hash, destHash) {
		t.Fatalf("remote destination hash = %x, want %x", remoteDest.Hash, destHash)
	}

	link := mustTestNewLink(t, ts, remoteDest)
	established := make(chan struct{}, 1)
	link.SetLinkEstablishedCallback(func(*Link) { established <- struct{}{} })
	if err := link.Establish(); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	select {
	case <-established:
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for link establishment")
	}

	requestPayload := resourceHMUIntegrationData(3)
	wantResponse := resourceHMUIntegrationData(7)
	responses := make(chan []byte, 1)
	failures := make(chan struct{}, 1)
	if _, err := link.Request("hmu_path", requestPayload,
		func(rr *RequestReceipt) {
			if data, ok := rr.Response.([]byte); ok {
				responses <- data
				return
			}
			failures <- struct{}{}
		},
		func(*RequestReceipt) { failures <- struct{}{} },
		nil, 90*time.Second, 0); err != nil {
		t.Fatalf("Request: %v", err)
	}

	select {
	case got := <-responses:
		if !bytes.Equal(got, wantResponse) {
			t.Fatalf("response payload differs: got %v bytes (sha256 %v), want %v bytes (sha256 %v)",
				len(got), resourceHMUIntegrationDigest(got), len(wantResponse), resourceHMUIntegrationDigest(wantResponse))
		}
	case <-failures:
		t.Fatal("request failed")
	case <-time.After(90 * time.Second):
		t.Fatal("timed out waiting for the response")
	}

	// The reference must have received the Go sender's payload intact; that is
	// the assertion that the truncated advertisement plus HMU exchange carried
	// every part.
	wantDigest := "REQUEST DIGEST: " + resourceHMUIntegrationDigest(requestPayload)
	pyMu.Lock()
	found := false
	for _, line := range pyLines {
		if line == wantDigest {
			found = true
			break
		}
	}
	pyMu.Unlock()
	if !found {
		t.Errorf("Python did not report %q; a truncated advertisement or a lost HMU segment would leave its reassembly incomplete", wantDigest)
	}
}

// TestIntegratedResourceHMUPythonToGo is the reverse direction: the installed
// Python RNS sends the >50 KB request (its sender truncates the advertisement
// and the Go receiver requests RESOURCE_HMU) and receives the Go side's >50 KB
// response.
func TestIntegratedResourceHMUPythonToGo(t *testing.T) {
	testutils.SkipShortIntegration(t)
	testutils.SkipIfNoPythonRNS(t)

	tmpDir := testutils.TempDir(t, "go-reticulum-resource-hmu-*")
	script := writePythonResourceHMUScript(t, tmpDir)
	pyListenPort, goListenPort := allocateUDPPortPair(t)

	ts, _ := startGoResourceHMUTransport(t, tmpDir, goListenPort, pyListenPort)

	identity, err := NewIdentity(true, ts.GetLogger())
	if err != nil {
		t.Fatal(err)
	}
	dest := mustTestNewDestination(t, ts, identity, DestinationIn, DestinationSingle, "integrated_test", "hmu_resource")

	requestPayload := resourceHMUIntegrationData(3)
	responsePayload := resourceHMUIntegrationData(7)
	received := make(chan []byte, 1)
	dest.RegisterRequestHandler("hmu_path", func(path string, data any, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
		payload, ok := data.([]byte)
		if !ok {
			received <- nil
			return nil
		}
		received <- append([]byte(nil), payload...)
		return responsePayload
	}, AllowAll, nil, false)
	stopAnnouncing := startGoAnnouncer(t, dest)
	defer stopAnnouncing()

	pyConfigDir := filepath.Join(tmpDir, "py_initiator")
	_, scanner := startPythonResourceHMUPeer(t, script, "initiator", pyConfigDir,
		fmt.Sprintf("%d", pyListenPort), fmt.Sprintf("%d", goListenPort),
		fmt.Sprintf("%x", dest.Hash), fmt.Sprintf("%x", identity.GetPublicKey()))

	var requestDigest, responseDigest string
	done := make(chan struct{})
	var mu sync.Mutex
	go func() {
		defer close(done)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[Python Initiator] %v\n", line)
			mu.Lock()
			if strings.HasPrefix(line, "REQUEST DIGEST: ") {
				requestDigest = strings.TrimPrefix(line, "REQUEST DIGEST: ")
			} else if strings.HasPrefix(line, "RESPONSE DIGEST: ") {
				responseDigest = strings.TrimPrefix(line, "RESPONSE DIGEST: ")
			}
			mu.Unlock()
		}
	}()

	select {
	case got := <-received:
		if !bytes.Equal(got, requestPayload) {
			t.Fatalf("Go received a %v-byte payload (sha256 %v), want %v bytes (sha256 %v)",
				len(got), resourceHMUIntegrationDigest(got), len(requestPayload), resourceHMUIntegrationDigest(requestPayload))
		}
	case <-time.After(90 * time.Second):
		t.Fatal("timed out waiting for the Python initiator's request")
	}

	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatal("timed out waiting for the Python initiator to finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if requestDigest != resourceHMUIntegrationDigest(requestPayload) {
		t.Errorf("Python request digest = %q, want %q", requestDigest, resourceHMUIntegrationDigest(requestPayload))
	}
	if responseDigest != resourceHMUIntegrationDigest(responsePayload) {
		t.Errorf("Python response digest = %q, want %q; the Go sender's truncated advertisement plus HMU reply did not reassemble", responseDigest, resourceHMUIntegrationDigest(responsePayload))
	}
}
