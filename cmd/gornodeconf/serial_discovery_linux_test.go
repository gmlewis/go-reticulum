// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.
//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestDiscoverRnodeSerialPortPrefersJTAGSymlink(t *testing.T) {
	t.Parallel()

	root := tempSerialDiscoveryDir(t)
	mustSymlink(t, filepath.Join(root, "usb-other-device-if00"), "../../ttyUSB1")
	mustSymlink(t, filepath.Join(root, "usb-Espressif_USB_JTAG_serial_debug_unit_8C:FD:49:B6:52:68-if00"), "../../ttyACM0")

	state := newTestSerialDiscoveryState(root)
	port, candidates, err := state.discover()
	if err != nil {
		t.Fatalf("discover returned error: %v", err)
	}
	if port != "/dev/ttyACM0" {
		t.Fatalf("port mismatch: got %q", port)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %v", candidates)
	}
}

func TestDiscoverRnodeSerialPortFallsBackToSingleCandidate(t *testing.T) {
	t.Parallel()

	root := tempSerialDiscoveryDir(t)
	mustSymlink(t, filepath.Join(root, "usb-plain-device-if00"), "../../ttyUSB3")

	state := newTestSerialDiscoveryState(root)
	port, candidates, err := state.discover()
	if err != nil {
		t.Fatalf("discover returned error: %v", err)
	}
	if port != "/dev/ttyUSB3" {
		t.Fatalf("port mismatch: got %q", port)
	}
	if len(candidates) != 1 || candidates[0] != "/dev/ttyUSB3" {
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
	if !strings.Contains(err.Error(), "no likely RNode device") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveLivePortUsesDiscoveredPort(t *testing.T) {
	t.Parallel()

	rt := cliRuntime{discoverPort: func() (string, []string, error) { return "/dev/ttyACM0", []string{"/dev/ttyACM0"}, nil }}

	port, err := rt.resolveLivePort("", options{sign: true})
	if err != nil {
		t.Fatalf("resolveLivePort returned error: %v", err)
	}
	if port != "/dev/ttyACM0" {
		t.Fatalf("port mismatch: got %q", port)
	}
}

func tempSerialDiscoveryDir(t *testing.T) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-discovery-*")
	t.Cleanup(cleanup)
	return dir
}

func mustSymlink(t *testing.T, linkName, target string) {
	t.Helper()

	if err := os.Symlink(target, linkName); err != nil {
		t.Fatalf("create symlink %v -> %v: %v", linkName, target, err)
	}
}

func newTestSerialDiscoveryState(root string) *serialDiscoveryState {
	return &serialDiscoveryState{
		root: "/dev/serial/by-id",
		readDir: func(string) ([]os.DirEntry, error) {
			return os.ReadDir(root)
		},
		readLink: func(linkPath string) (string, error) {
			return os.Readlink(filepath.Join(root, filepath.Base(linkPath)))
		},
	}
}
