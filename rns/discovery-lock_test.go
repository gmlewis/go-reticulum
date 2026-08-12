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

// TestDiscoveryLoadHoldsDiscoveryLock covers Phase 11 task 8: the load path
// (ListDiscoveredInterfaces) serializes its per-file read under discoveryLock,
// mirroring Python's `with self.discovery_lock:` around the open+unpackb
// (RNS/Discovery.py:472-474). While the lock is externally held, a load
// goroutine must block until the lock is released.
func TestDiscoveryLoadHoldsDiscoveryLock(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-discovery-lock-load-")
	storagePath := filepath.Join(tmpDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	now := float64(time.Now().UnixNano()) / 1e9
	if err := os.WriteFile(filepath.Join(storagePath, "valid.data"), mustMsgpackPack(map[string]any{
		"name":         "Valid",
		"type":         "TCPServerInterface",
		"last_heard":   now - 60,
		"transport":    true,
		"value":        1,
		"transport_id": "deadbeefdeadbeefdeadbeefdeadbeef",
		"network_id":   "feedface",
	}), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	id := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir, logger: NewLogger()})

	// Hold the lock so the load goroutine cannot enter its read section.
	id.discoveryLock.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := id.ListDiscoveredInterfaces(false, false); err != nil {
			t.Errorf("ListDiscoveredInterfaces: %v", err)
		}
	}()
	select {
	case <-done:
		t.Fatal("ListDiscoveredInterfaces completed without waiting on discoveryLock")
	case <-time.After(50 * time.Millisecond):
	}
	id.discoveryLock.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ListDiscoveredInterfaces did not complete after discoveryLock release")
	}
}

// TestDiscoveryPersistHoldsDiscoveryLock covers Phase 11 task 8: the persist
// path (persistDiscoveredInterface) serializes its stat+read+write under
// discoveryLock, mirroring Python's `with self.discovery_lock:` block in
// interface_discovered (RNS/Discovery.py:533-562). While the lock is
// externally held, a persist goroutine must block until the lock is released.
func TestDiscoveryPersistHoldsDiscoveryLock(t *testing.T) {
	t.Parallel()
	tmpDir := testutils.TempDir(t, "rns-discovery-lock-persist-")
	id := NewInterfaceDiscovery(&Reticulum{configDir: tmpDir, logger: NewLogger()})

	now := float64(time.Now().UnixNano()) / 1e9
	info := map[string]any{
		"discovery_hash": []byte("persist-lock-hash!"),
		"received":       now,
		"name":           "PersistLock",
		"type":           "TCPServerInterface",
		"value":          1,
		"transport":      true,
		"transport_id":   "deadbeefdeadbeefdeadbeefdeadbeef",
		"network_id":     "feedface",
	}

	id.discoveryLock.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := id.persistDiscoveredInterface(info); err != nil {
			t.Errorf("persistDiscoveredInterface: %v", err)
		}
	}()
	select {
	case <-done:
		t.Fatal("persistDiscoveredInterface completed without waiting on discoveryLock")
	case <-time.After(50 * time.Millisecond):
	}
	id.discoveryLock.Unlock()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("persistDiscoveredInterface did not complete after discoveryLock release")
	}
}
