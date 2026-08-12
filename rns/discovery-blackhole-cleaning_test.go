// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// discoveryBlackholeHash is a 16-byte identity hash used as a blackholed
// transport_id / network_id in the Task 12 load-removal tests.
var discoveryBlackholeHash = []byte{
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
	0xde, 0xad, 0xbe, 0xef, 0xde, 0xad, 0xbe, 0xef,
}

// discoveryBlackholeHex is the hex-encoded form of discoveryBlackholeHash,
// matching how Python persists transport_id/network_id (RNS.hexrep,
// RNS/Discovery.py:323-324).
const discoveryBlackholeHex = "deadbeefdeadbeefdeadbeefdeadbeef"

// discoveryValidTransportIDHex is a non-blackholed 16-byte transport_id used
// for entries that must survive the load removal chain.
const discoveryValidTransportIDHex = "0102030405060708090a0b0c0d0e0f10"

// discoveryValidNetworkIDHex is a non-blackholed network_id used for entries
// that must survive the load removal chain.
const discoveryValidNetworkIDHex = "0a0b0c0d0e0f10111213141516171819"

// newDiscoveryLoadRemovalTestReticulum builds a Reticulum whose transport
// registry has discoveryBlackholeHash blackholed, for Task 12 load-removal
// tests. The discovery sources set is empty so the network_id membership
// check (RNS/Discovery.py:487) is skipped.
func newDiscoveryLoadRemovalTestReticulum(t *testing.T) (*Reticulum, *TransportSystem, string) {
	t.Helper()
	tmpDir := testutils.TempDir(t, "rns-discovery-blackhole-")
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("failed to create storage path: %v", err)
	}
	logger := NewLogger()
	ts := NewTransportSystem(logger)
	if !ts.BlackholeIdentity(discoveryBlackholeHash, nil, "test") {
		t.Fatal("BlackholeIdentity() = false, want true")
	}
	r := &Reticulum{configDir: tmpDir, transport: ts, logger: logger}
	return r, ts, storagePath
}

func writeDiscoveryLoadFile(t *testing.T, storagePath, name string, fields map[string]any) {
	t.Helper()
	now := float64(time.Now().UnixNano()) / 1e9
	m := map[string]any{
		"name":       name,
		"type":       "TCPServerInterface",
		"last_heard": now - 60,
		"transport":  true,
		"value":      1,
	}
	maps.Copy(m, fields)
	if err := os.WriteFile(filepath.Join(storagePath, name+".data"), mustMsgpackPack(m), 0o644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}
}

// TestDiscoveryLoadRemovesBlackholedTransportID covers Phase 11 task 12: an
// entry whose transport_id is a blackholed identity hash is removed on load
// (RNS/Discovery.py:490).
func TestDiscoveryLoadRemovesBlackholedTransportID(t *testing.T) {
	t.Parallel()
	r, _, storagePath := newDiscoveryLoadRemovalTestReticulum(t)
	writeDiscoveryLoadFile(t, storagePath, "blackholed-transport", map[string]any{
		"transport_id": discoveryBlackholeHex,
		"network_id":   discoveryValidNetworkIDHex,
	})
	discovery := NewInterfaceDiscovery(r)
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected 0 discovered interfaces (blackholed transport_id), got %v", len(discovered))
	}
	if _, err := os.Stat(filepath.Join(storagePath, "blackholed-transport.data")); !os.IsNotExist(err) {
		t.Fatalf("expected blackholed-transport.data removed, stat err = %v", err)
	}
}

// TestDiscoveryLoadRemovesBlackholedNetworkID covers Phase 11 task 12: an
// entry whose network_id is a blackholed identity hash is removed on load
// (RNS/Discovery.py:489).
func TestDiscoveryLoadRemovesBlackholedNetworkID(t *testing.T) {
	t.Parallel()
	r, _, storagePath := newDiscoveryLoadRemovalTestReticulum(t)
	writeDiscoveryLoadFile(t, storagePath, "blackholed-network", map[string]any{
		"transport_id": discoveryValidTransportIDHex,
		"network_id":   discoveryBlackholeHex,
	})
	discovery := NewInterfaceDiscovery(r)
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected 0 discovered interfaces (blackholed network_id), got %v", len(discovered))
	}
	if _, err := os.Stat(filepath.Join(storagePath, "blackholed-network.data")); !os.IsNotExist(err) {
		t.Fatalf("expected blackholed-network.data removed, stat err = %v", err)
	}
}

// TestDiscoveryLoadRemovesMissingTransportID covers Phase 11 task 12: an
// entry missing transport_id entirely is removed on load
// (RNS/Discovery.py:484).
func TestDiscoveryLoadRemovesMissingTransportID(t *testing.T) {
	t.Parallel()
	r, _, storagePath := newDiscoveryLoadRemovalTestReticulum(t)
	writeDiscoveryLoadFile(t, storagePath, "missing-transport", map[string]any{
		"network_id": discoveryValidNetworkIDHex,
	})
	discovery := NewInterfaceDiscovery(r)
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected 0 discovered interfaces (missing transport_id), got %v", len(discovered))
	}
	if _, err := os.Stat(filepath.Join(storagePath, "missing-transport.data")); !os.IsNotExist(err) {
		t.Fatalf("expected missing-transport.data removed, stat err = %v", err)
	}
}

// TestDiscoveryLoadRemovesMissingNetworkID covers Phase 11 task 12: an entry
// missing network_id entirely is removed on load (RNS/Discovery.py:485).
func TestDiscoveryLoadRemovesMissingNetworkID(t *testing.T) {
	t.Parallel()
	r, _, storagePath := newDiscoveryLoadRemovalTestReticulum(t)
	writeDiscoveryLoadFile(t, storagePath, "missing-network", map[string]any{
		"transport_id": discoveryValidTransportIDHex,
	})
	discovery := NewInterfaceDiscovery(r)
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected 0 discovered interfaces (missing network_id), got %v", len(discovered))
	}
	if _, err := os.Stat(filepath.Join(storagePath, "missing-network.data")); !os.IsNotExist(err) {
		t.Fatalf("expected missing-network.data removed, stat err = %v", err)
	}
}

// TestDiscoveryLoadKeepsValidNonBlackholedEntry covers Phase 11 task 12: an
// entry with present, non-blackholed transport_id and network_id survives
// the load removal chain and is returned.
func TestDiscoveryLoadKeepsValidNonBlackholedEntry(t *testing.T) {
	t.Parallel()
	r, _, storagePath := newDiscoveryLoadRemovalTestReticulum(t)
	writeDiscoveryLoadFile(t, storagePath, "valid-entry", map[string]any{
		"transport_id": discoveryValidTransportIDHex,
		"network_id":   discoveryValidNetworkIDHex,
	})
	discovery := NewInterfaceDiscovery(r)
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces() error = %v", err)
	}
	if got, want := len(discovered), 1; got != want {
		t.Fatalf("expected %v discovered interfaces (valid entry), got %v", want, got)
	}
	if _, err := os.Stat(filepath.Join(storagePath, "valid-entry.data")); err != nil {
		t.Fatalf("expected valid-entry.data to remain on disk, stat err = %v", err)
	}
}
