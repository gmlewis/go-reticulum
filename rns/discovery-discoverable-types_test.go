// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestDiscoveryLoadRemovesBogusType verifies that the load path
// (ListDiscoveredInterfaces) removes persisted entries whose type is not in
// DISCOVERABLE_TYPES (RNS/Discovery.py:488). A hand-written .data file with a
// bogus type is removed during a load cycle.
func TestDiscoveryLoadRemovesBogusType(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-discovery-bogus-type-")
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	packed := mustMsgpackPack(map[string]any{
		"name":         "Bogus",
		"type":         "BogusInterface",
		"last_heard":   now - 60,
		"transport":    true,
		"value":        1,
		"transport_id": "deadbeefdeadbeefdeadbeefdeadbeef",
		"network_id":   "feedface",
	})
	path := filepath.Join(storagePath, "bogus.data")
	if err := os.WriteFile(path, packed, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir, logger: NewLogger()})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("len(discovered) = %v, want 0 (bogus type removed)", len(discovered))
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("expected bogus-type discovery file to be removed")
	}
}

// TestDiscoveryLoadRemovesMissingType verifies that a persisted entry with no
// type field at all is removed (RNS/Discovery.py:488, `not "type" in info`).
func TestDiscoveryLoadRemovesMissingType(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-discovery-missing-type-")
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	packed := mustMsgpackPack(map[string]any{
		"name":         "No Type",
		"last_heard":   now - 60,
		"transport":    true,
		"value":        1,
		"transport_id": "deadbeefdeadbeefdeadbeefdeadbeef",
		"network_id":   "feedface",
	})
	path := filepath.Join(storagePath, "notype.data")
	if err := os.WriteFile(path, packed, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir, logger: NewLogger()})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("len(discovered) = %v, want 0 (missing type removed)", len(discovered))
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("expected missing-type discovery file to be removed")
	}
}

// TestDiscoveryLoadKeepsValidType verifies that entries with a type in
// DISCOVERABLE_TYPES are kept (the filter does not over-remove).
func TestDiscoveryLoadKeepsValidType(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-discovery-valid-type-")
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	packed := mustMsgpackPack(map[string]any{
		"name":         "Valid",
		"type":         "TCPServerInterface",
		"last_heard":   now - 60,
		"transport":    true,
		"value":        1,
		"transport_id": "deadbeefdeadbeefdeadbeefdeadbeef",
		"network_id":   "feedface",
	})
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), packed, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir, logger: NewLogger()})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces: %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("len(discovered) = %v, want 1 (valid type kept)", len(discovered))
	}
}

// TestDiscoveryLoadRejectsTCPClientType verifies that the load path uses
// DISCOVERABLE_TYPES (6 types, no TCPClientInterface), so a persisted
// TCPClientInterface entry is removed even though TCPClientInterface IS
// accepted on the receive path (DISCOVERABLE_INTERFACE_TYPES, 7 types).
func TestDiscoveryLoadRejectsTCPClientType(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-discovery-tcpclient-type-")
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	packed := mustMsgpackPack(map[string]any{
		"name":         "TCPClient",
		"type":         "TCPClientInterface",
		"last_heard":   now - 60,
		"transport":    true,
		"value":        1,
		"transport_id": "deadbeefdeadbeefdeadbeefdeadbeef",
		"network_id":   "feedface",
	})
	path := filepath.Join(storagePath, "tcpclient.data")
	if err := os.WriteFile(path, packed, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	discovery := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir, logger: NewLogger()})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("ListDiscoveredInterfaces: %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("len(discovered) = %v, want 0 (TCPClientInterface not in DISCOVERABLE_TYPES)", len(discovered))
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("expected TCPClientInterface discovery file to be removed")
	}
}
