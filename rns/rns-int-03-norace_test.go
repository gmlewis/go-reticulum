// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && !race
// +build integration,!race

package rns

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestIntegratedResponseResourceCompressionPolicyGoToPython(t *testing.T) {
	testutils.SkipShortIntegration(t)

	cases := []struct {
		name     string
		autoMode string
	}{
		{name: "AutoCompressTrue", autoMode: "true"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tmpDir, cleanup := testutils.TempDir(t, "go-reticulum-policy-go2py-*")
			defer cleanup()

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

			logger := NewLogger()
			logger.SetLogLevel(LogDebug)
			ts := NewTransportSystem(logger)
			r := mustTestNewReticulumWithLogger(t, ts, goConfigDir, logger)
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
