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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

const integratedBzip2PolicyResponderPy = `import RNS
import sys
import time
import os


def parse_auto_mode(mode):
    if mode == "true":
        return True
    if mode == "false":
        return False
    if mode.startswith("limit:"):
        return int(mode.split(":", 1)[1])
    raise ValueError(f"invalid mode: {mode}")


def start_responder(config_dir, listen_port, forward_port, auto_mode, payload_size):
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

    storage_dir = os.path.join(config_dir, "storage")
    if os.path.exists(storage_dir):
        import shutil
        shutil.rmtree(storage_dir)

    reticulum = RNS.Reticulum(configdir=config_dir, loglevel=RNS.LOG_EXTREME)
    RNS.logdest = RNS.LOG_STDOUT

    identity = RNS.Identity()
    destination = RNS.Destination(identity, RNS.Destination.IN, RNS.Destination.SINGLE, "integrated_test", "parity")

    auto_compress = parse_auto_mode(auto_mode)

    def request_handler(path, data, request_id, link_id, remote_identity, requested_at):
        return b"Z" * int(payload_size)

    destination.register_request_handler("test_path", request_handler, RNS.Destination.ALLOW_ALL, None, auto_compress)

    print(f"Destination Hash: {destination.hash.hex()}")
    print(f"Identity Public Key: {identity.get_public_key().hex()}")
    sys.stdout.flush()

    done_file = os.path.join(config_dir, "done")
    if os.path.exists(done_file):
        os.remove(done_file)

    timeout = time.time() + 25
    while time.time() < timeout:
        destination.announce()
        time.sleep(0.5)
        if os.path.exists(done_file):
            print("Responder done")
            sys.stdout.flush()
            return


if __name__ == "__main__":
    if len(sys.argv) != 6:
        print("Usage: responder.py <config_dir> <listen_port> <forward_port> <auto_mode> <payload_size>")
        sys.exit(1)
    start_responder(sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), sys.argv[4], int(sys.argv[5]))
`

const integratedBzip2PolicyRequesterPy = `import RNS
import sys
import time
import os


def start_requester(dest_hash_hex, pub_key_hex, config_dir, listen_port, forward_port):
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

    dest_hash = bytes.fromhex(dest_hash_hex)
    pub_key = bytes.fromhex(pub_key_hex)

    timeout = time.time() + 12
    while not RNS.Transport.has_path(dest_hash) and time.time() < timeout:
        time.sleep(0.2)

    if not RNS.Transport.has_path(dest_hash):
        print("ERROR:PATH_TIMEOUT")
        sys.stdout.flush()
        sys.exit(1)

    remote_identity = RNS.Identity(create_keys=False)
    remote_identity.load_public_key(pub_key)
    destination = RNS.Destination(remote_identity, RNS.Destination.OUT, RNS.Destination.SINGLE, "integrated_test", "parity")

    link = RNS.Link(destination)
    linked = [False]
    resource_compressed = [None]
    response_len = [None]

    def established(l):
        linked[0] = True

    def resource_started(resource):
        resource_compressed[0] = 1 if resource.is_compressed() else 0

    def response_callback(rr):
        data = rr.response if hasattr(rr, "response") else rr
        response_len[0] = len(data)

    link.set_link_established_callback(established)
    link.set_resource_started_callback(resource_started)

    timeout = time.time() + 12
    while not linked[0] and time.time() < timeout:
        time.sleep(0.2)

    if not linked[0]:
        print("ERROR:LINK_TIMEOUT")
        sys.stdout.flush()
        sys.exit(1)

    link.request("test_path", b"req", response_callback)

    timeout = time.time() + 20
    while response_len[0] is None and time.time() < timeout:
        time.sleep(0.2)

    if response_len[0] is None:
        print("ERROR:RESPONSE_TIMEOUT")
        sys.stdout.flush()
        sys.exit(1)

    if resource_compressed[0] is None:
        resource_compressed[0] = 0

    print(f"RESOURCE_COMPRESSED:{resource_compressed[0]}")
    print(f"RESPONSE_LEN:{response_len[0]}")
    sys.stdout.flush()


if __name__ == "__main__":
    if len(sys.argv) != 6:
        print("Usage: requester.py <dest_hash_hex> <pub_key_hex> <config_dir> <listen_port> <forward_port>")
        sys.exit(1)
    start_requester(sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4]), int(sys.argv[5]))
`

func TestIntegratedResponseResourceCompressionPolicyGoToPython(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}
	if RaceEnabled {
		t.Skip("Skipping in race mode due known concurrent hash access in request path under this stress pattern")
	}

	cases := []struct {
		name     string
		autoMode string
	}{
		{name: "AutoCompressTrue", autoMode: "true"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "go-reticulum-policy-go2py-*")
			mustTest(t, err)
			t.Cleanup(func() {
				if err := os.RemoveAll(tmpDir); err != nil {
					t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
				}
			})

			pyListenPort, goListenPort := allocateUDPPortPair(t)

			scriptPath := filepath.Join(tmpDir, "policy_responder.py")
			if err := os.WriteFile(scriptPath, []byte(integratedBzip2PolicyResponderPy), 0o644); err != nil {
				t.Fatal(err)
			}
			pyConfigDir := filepath.Join(tmpDir, "py_responder")

			payloadSize := MDU + 768
			pyCmd := exec.Command("python3", scriptPath, pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort), tc.autoMode, strconv.Itoa(payloadSize))
			pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
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

			scanner := bufio.NewScanner(pyStdout)
			var destHashHex, pyPubHex string
			identityLines := make([]string, 0, 16)
			for scanner.Scan() {
				line := scanner.Text()
				identityLines = append(identityLines, line)
				if strings.HasPrefix(line, "Destination Hash:") {
					destHashHex = strings.TrimSpace(strings.TrimPrefix(line, "Destination Hash:"))
				}
				if strings.HasPrefix(line, "Identity Public Key:") {
					pyPubHex = strings.TrimSpace(strings.TrimPrefix(line, "Identity Public Key:"))
					break
				}
			}
			if destHashHex == "" || pyPubHex == "" {
				t.Fatalf("failed to parse python responder identity output; lines=%q", identityLines)
			}

			destHash, err := HexToBytes(destHashHex)
			if err != nil {
				t.Fatalf("parse destination hash: %v", err)
			}
			pyPub, err := HexToBytes(pyPubHex)
			if err != nil {
				t.Fatalf("parse python pubkey: %v", err)
			}

			goConfigDir := filepath.Join(tmpDir, "go_rns")
			if err := os.MkdirAll(goConfigDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)), 0o600); err != nil {
				t.Fatal(err)
			}

			SetLogLevel(LogDebug)
			ts := NewTransportSystem()
			r := mustTestNewReticulum(t, ts, goConfigDir)
			defer closeReticulum(t, r)

			transport := r.Transport()
			pathDeadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(pathDeadline) {
				if transport.HasPath(destHash) {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !transport.HasPath(destHash) {
				t.Fatal("timed out waiting for python path")
			}

			remoteID := mustTestNewIdentity(t, false)
			if err := remoteID.LoadPublicKey(pyPub); err != nil {
				t.Fatalf("load python public key: %v", err)
			}
			remoteDest := mustTestNewDestination(t, ts, remoteID, DestinationOut, DestinationSingle, "integrated_test", "parity")
			if !bytes.Equal(remoteDest.Hash, destHash) {
				t.Fatalf("destination hash mismatch: expected %x got %x", destHash, remoteDest.Hash)
			}

			link := mustTestNewLink(t, ts, remoteDest)
			var mu sync.Mutex
			var gotCompressed *bool
			link.SetResourceStartedCallback(func(r *Resource) {
				if r == nil || !r.isResponse {
					return
				}
				v := r.compressed
				mu.Lock()
				gotCompressed = &v
				mu.Unlock()
			})
			linked := make(chan struct{}, 1)
			link.SetLinkEstablishedCallback(func(*Link) { linked <- struct{}{} })
			if err := link.Establish(); err != nil {
				t.Fatalf("establish link: %v", err)
			}
			select {
			case <-linked:
			case <-time.After(10 * time.Second):
				t.Fatal("timed out waiting for link establishment")
			}

			responseReady := make(chan []byte, 1)
			if _, err := link.Request("test_path", []byte("req"), func(rr *RequestReceipt) {
				responseReady <- rr.Response.([]byte)
			}, nil, nil, 0); err != nil {
				t.Fatalf("request failed: %v", err)
			}

			select {
			case resp := <-responseReady:
				if len(resp) != payloadSize {
					t.Fatalf("response size mismatch: got %v want %v", len(resp), payloadSize)
				}
				mu.Lock()
				gc := gotCompressed
				mu.Unlock()
				if gc == nil {
					t.Fatal("did not observe response resource start callback")
				}
				if !*gc {
					t.Fatal("expected compressed response resource from python responder")
				}
			case <-time.After(20 * time.Second):
				t.Fatal("timed out waiting for response")
			}

			_ = os.WriteFile(filepath.Join(pyConfigDir, "done"), []byte("done"), 0o644)
			if err := pyCmd.Wait(); err != nil {
				t.Fatalf("python responder failed: %v", err)
			}
		})
	}
}

func setupGoOnlyIntegrationLinkPair(t *testing.T) (*Link, *Link) {
	t.Helper()

	tsInitiator := &TransportSystem{
		pathTable:    make(map[string]*PathEntry),
		packetHashes: make(map[string]time.Time),
		destinations: make([]*Destination, 0),
		pendingLinks: make([]*Link, 0),
		activeLinks:  make([]*Link, 0),
	}
	idInitiator := mustTestNewIdentity(t, true)
	tsInitiator.identity = idInitiator

	tsReceiver := &TransportSystem{
		pathTable:    make(map[string]*PathEntry),
		packetHashes: make(map[string]time.Time),
		destinations: make([]*Destination, 0),
		pendingLinks: make([]*Link, 0),
		activeLinks:  make([]*Link, 0),
	}
	idReceiver := mustTestNewIdentity(t, true)
	tsReceiver.identity = idReceiver

	var pipeInitiator, pipeReceiver *interfaces.PipeInterface
	pipeInitiator = interfaces.NewPipeInterface("initiator", func(data []byte, iface interfaces.Interface) {
		tsInitiator.Inbound(data, iface)
	})
	pipeReceiver = interfaces.NewPipeInterface("receiver", func(data []byte, iface interfaces.Interface) {
		tsReceiver.Inbound(data, iface)
	})
	pipeInitiator.SetOther(pipeReceiver)
	pipeReceiver.SetOther(pipeInitiator)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest := mustTestNewDestination(t, tsReceiver, idReceiver, DestinationIn, DestinationSingle, "integrated_test", "parity")

	receiverEstablished := make(chan *Link, 1)
	receiverDest.SetLinkEstablishedCallback(func(l *Link) {
		receiverEstablished <- l
	})

	initiatorLink := mustTestNewLink(t, tsInitiator, receiverDest)
	initiatorEstablished := make(chan struct{}, 1)
	initiatorLink.SetLinkEstablishedCallback(func(*Link) {
		initiatorEstablished <- struct{}{}
	})

	if err := initiatorLink.Establish(); err != nil {
		t.Fatalf("failed to establish link: %v", err)
	}

	select {
	case <-initiatorEstablished:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}

	var receiverLink *Link
	select {
	case receiverLink = <-receiverEstablished:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for receiver link establishment")
	}

	return initiatorLink, receiverLink
}

func TestIntegratedGoOnlyLargeResourceCompressionOnOff(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	tests := []struct {
		name              string
		autoCompress      bool
		autoCompressLimit int
		expectCompressed  bool
	}{
		{name: "AutoCompressOn", autoCompress: true, autoCompressLimit: ResourceAutoCompressMaxSize, expectCompressed: true},
		{name: "AutoCompressOff", autoCompress: false, autoCompressLimit: 0, expectCompressed: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			initiatorLink, receiverLink := setupGoOnlyIntegrationLinkPair(t)

			receiverLink.destination.RegisterRequestHandlerWithAutoCompressLimit(
				"/test/path",
				func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
					return bytes.Repeat([]byte("R"), initiatorLink.mdu+1024)
				},
				AllowAll,
				nil,
				tc.autoCompress,
				tc.autoCompressLimit,
			)

			var mu sync.Mutex
			var gotCompressed *bool
			initiatorLink.SetResourceStartedCallback(func(r *Resource) {
				if r == nil || !r.isResponse {
					return
				}
				v := r.compressed
				mu.Lock()
				gotCompressed = &v
				mu.Unlock()
			})

			responseReady := make(chan []byte, 1)
			if _, err := initiatorLink.Request("/test/path", []byte("req"), func(rr *RequestReceipt) {
				responseReady <- rr.Response.([]byte)
			}, nil, nil, 0); err != nil {
				t.Fatalf("request failed: %v", err)
			}

			select {
			case resp := <-responseReady:
				if len(resp) != initiatorLink.mdu+1024 {
					t.Fatalf("response size mismatch: got %v want %v", len(resp), initiatorLink.mdu+1024)
				}
				mu.Lock()
				gc := gotCompressed
				mu.Unlock()
				if gc == nil {
					t.Fatal("did not observe response resource callback")
				}
				if *gc != tc.expectCompressed {
					t.Fatalf("compressed flag mismatch: got %v want %v", *gc, tc.expectCompressed)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("timeout waiting for response")
			}
		})
	}
}

func TestIntegratedGoOnlyChannelStreamCompressedChunks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	initiatorLink, receiverLink := setupGoOnlyIntegrationLinkPair(t)

	txChannel := initiatorLink.GetChannel()
	rxChannel := receiverLink.GetChannel()

	var sawCompressed bool
	rxChannel.RegisterMessageType(SMTStreamData, func() Message { return &StreamDataMessage{} })
	rxChannel.addMessageHandler(func(msg Message) bool {
		sm, ok := msg.(*StreamDataMessage)
		if !ok {
			return false
		}
		if sm.Compressed {
			sawCompressed = true
		}
		return false
	})

	reader := Buffer.CreateReader(7, rxChannel)
	writer := Buffer.CreateWriterWithOptions(7, txChannel, ChannelWriterOptions{EnableCompression: true})

	original := bytes.Repeat([]byte("stream-compress-me-"), 700)
	if _, err := writer.Write(original); err != nil {
		t.Fatalf("stream write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("stream close failed: %v", err)
	}

	received, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("stream read failed: %v", err)
	}

	if !bytes.Equal(received, original) {
		t.Fatalf("stream payload mismatch: got %v bytes want %v", len(received), len(original))
	}
	if !sawCompressed {
		t.Fatal("expected at least one compressed StreamDataMessage chunk")
	}
}

func TestIntegratedResponseResourceCompressionPolicyPythonToGo(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integrated test in short mode")
	}

	cases := []struct {
		name              string
		autoCompress      bool
		autoCompressLimit int
		expectCompressed  bool
	}{
		{name: "AutoCompressTrue", autoCompress: true, autoCompressLimit: ResourceAutoCompressMaxSize, expectCompressed: true},
		{name: "AutoCompressLimitSmall", autoCompress: true, autoCompressLimit: 32, expectCompressed: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "go-reticulum-policy-py2go-*")
			mustTest(t, err)
			t.Cleanup(func() {
				if err := os.RemoveAll(tmpDir); err != nil {
					t.Logf("failed to remove temp dir %v: %v", tmpDir, err)
				}
			})

			pyListenPort, goListenPort := allocateUDPPortPair(t)

			goConfigDir := filepath.Join(tmpDir, "go_rns")
			if err := os.MkdirAll(goConfigDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(goConfigDir, "config"), []byte(mustUDPConfig(t.Name(), goListenPort, pyListenPort, false)), 0o600); err != nil {
				t.Fatal(err)
			}

			SetLogLevel(LogDebug)
			ts := NewTransportSystem()
			r := mustTestNewReticulum(t, ts, goConfigDir)
			defer closeReticulum(t, r)

			id := mustTestNewIdentity(t, true)
			dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "integrated_test", "parity")

			payloadSize := MDU + 768
			dest.RegisterRequestHandlerWithAutoCompressLimit("test_path", func(path string, data []byte, requestID []byte, linkID []byte, remoteIdentity *Identity, requestedAt time.Time) any {
				return bytes.Repeat([]byte("G"), payloadSize)
			}, AllowAll, nil, tc.autoCompress, tc.autoCompressLimit)

			go func() {
				for {
					_ = dest.Announce(nil)
					time.Sleep(500 * time.Millisecond)
				}
			}()

			scriptPath := filepath.Join(tmpDir, "policy_requester.py")
			if err := os.WriteFile(scriptPath, []byte(integratedBzip2PolicyRequesterPy), 0o644); err != nil {
				t.Fatal(err)
			}
			pyConfigDir := filepath.Join(tmpDir, "py_requester")

			pyCmd := exec.Command("python3", scriptPath, fmt.Sprintf("%x", dest.Hash), fmt.Sprintf("%x", id.GetPublicKey()), pyConfigDir, strconv.Itoa(pyListenPort), strconv.Itoa(goListenPort))
			pyCmd.Env = append(os.Environ(), "PYTHONPATH="+getPythonPath())
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

			scanner := bufio.NewScanner(pyStdout)
			var gotCompressed *bool
			var gotLen int
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "ERROR:") {
					t.Fatalf("python requester error: %v", line)
				}
				if strings.HasPrefix(line, "RESOURCE_COMPRESSED:") {
					value := strings.TrimPrefix(line, "RESOURCE_COMPRESSED:")
					v := value == "1"
					gotCompressed = &v
				}
				if strings.HasPrefix(line, "RESPONSE_LEN:") {
					parsed, parseErr := strconv.Atoi(strings.TrimPrefix(line, "RESPONSE_LEN:"))
					if parseErr != nil {
						t.Fatalf("invalid response len line %q: %v", line, parseErr)
					}
					gotLen = parsed
					break
				}
			}

			if err := pyCmd.Wait(); err != nil {
				t.Fatalf("python requester failed: %v", err)
			}

			if gotCompressed == nil {
				t.Fatal("did not observe RESOURCE_COMPRESSED from python requester")
			}
			if *gotCompressed != tc.expectCompressed {
				t.Fatalf("compressed flag mismatch: got %v want %v", *gotCompressed, tc.expectCompressed)
			}
			if gotLen != payloadSize {
				t.Fatalf("response size mismatch: got %v want %v", gotLen, payloadSize)
			}
		})
	}
}
