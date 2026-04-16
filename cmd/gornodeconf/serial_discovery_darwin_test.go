// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.
//
//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestDiscoverRnodeSerialPortPrefersQTagCUDevice(t *testing.T) {
	t.Parallel()

	root := tempSerialDiscoveryDir(t)
	mustCreateDeviceNode(t, filepath.Join(root, "tty.usbserial-generic"))
	mustCreateDeviceNode(t, filepath.Join(root, "cu.usbmodem-qtag-01"))

	state := newTestSerialDiscoveryState(root)
	port, candidates, err := state.discover()
	if err != nil {
		t.Fatalf("discover returned error: %v", err)
	}
	if port != "/dev/cu.usbmodem-qtag-01" {
		t.Fatalf("port mismatch: got %q", port)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %v", candidates)
	}
}

func TestDiscoverRnodeSerialPortFallsBackToSingleTTYCandidate(t *testing.T) {
	t.Parallel()

	root := tempSerialDiscoveryDir(t)
	mustCreateDeviceNode(t, filepath.Join(root, "tty.qtag-rnode"))

	state := newTestSerialDiscoveryState(root)
	port, candidates, err := state.discover()
	if err != nil {
		t.Fatalf("discover returned error: %v", err)
	}
	if port != "/dev/tty.qtag-rnode" {
		t.Fatalf("port mismatch: got %q", port)
	}
	if len(candidates) != 1 || candidates[0] != "/dev/tty.qtag-rnode" {
		t.Fatalf("candidate mismatch: %v", candidates)
	}
}

func TestResolveLivePortReportsHelpfulErrorWhenNoDeviceFound(t *testing.T) {
	t.Parallel()

	rt := cliRuntime{discoverPort: func() (string, []string, error) { return "", nil, nil }}

	_, err := rt.resolveLivePort("", options{sign: true})
	if err == nil {
		t.Fatal("expected helpful error")
	}
	if !strings.Contains(err.Error(), "/dev/cu.") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveLivePortUsesDiscoveredPort(t *testing.T) {
	t.Parallel()

	rt := cliRuntime{discoverPort: func() (string, []string, error) {
		return "/dev/cu.usbmodem-qtag-01", []string{"/dev/cu.usbmodem-qtag-01"}, nil
	}}

	port, err := rt.resolveLivePort("", options{sign: true})
	if err != nil {
		t.Fatalf("resolveLivePort returned error: %v", err)
	}
	if port != "/dev/cu.usbmodem-qtag-01" {
		t.Fatalf("port mismatch: got %q", port)
	}
}

func tempSerialDiscoveryDir(t *testing.T) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-discovery-*")
	t.Cleanup(cleanup)
	return dir
}

func mustCreateDeviceNode(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create device node %v: %v", path, err)
	}
}

func newTestSerialDiscoveryState(root string) *serialDiscoveryState {
	return &serialDiscoveryState{
		root: "/dev",
		readDir: func(string) ([]os.DirEntry, error) {
			return os.ReadDir(root)
		},
	}
}
