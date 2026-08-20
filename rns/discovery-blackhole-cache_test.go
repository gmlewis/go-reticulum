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
)

// discoveryBlackholeHash2 is a second 16-byte identity hash, blackholed AFTER
// the first ListDiscoveredInterfaces call, to prove the cached set is consulted
// rather than re-fetched every call.
var discoveryBlackholeHash2 = []byte{
	0xca, 0xfe, 0xba, 0xbe, 0xca, 0xfe, 0xba, 0xbe,
	0xca, 0xfe, 0xba, 0xbe, 0xca, 0xfe, 0xba, 0xbe,
}

// discoveryBlackholeHex2 is the hex-encoded form of discoveryBlackholeHash2.
const discoveryBlackholeHex2 = "cafebabecafebabecafebabecafebabe"

// TestDiscoveryBlackholedCacheConsultedAndRefreshedAfterTTL verifies that
// ListDiscoveredInterfaces consults a cached blackholed-identity set
// (RNS/Discovery.py:460-465, __blackholed / blackholed_updated) that is
// refreshed from GetBlackholedIdentities at most once per 60 seconds.
//
//  1. H1 is blackholed; the first call populates the cache {H1} and removes
//     the file whose transport_id is H1.
//  2. H2 is blackholed AFTER the first call. A second call within the TTL
//     must NOT refresh the cache, so the file whose transport_id is H2
//     survives (H2 is absent from the stale cache) — proving the cache is
//     consulted rather than re-fetched every call.
//  3. After the 60s TTL elapses, the next call refreshes the cache {H1,H2}
//     and removes the H2 file.
func TestDiscoveryBlackholedCacheConsultedAndRefreshedAfterTTL(t *testing.T) {
	t.Parallel()
	r, ts, storagePath := newDiscoveryLoadRemovalTestReticulum(t) // H1 blackholed
	discovery := NewInterfaceDiscovery(r)

	// F1: transport_id = H1 (blackholed). First call populates the cache and
	// removes F1.
	writeDiscoveryLoadFile(t, storagePath, "f1-blackholed", map[string]any{
		"transport_id": discoveryBlackholeHex,
		"network_id":   discoveryValidNetworkIDHex,
	})
	discovered, err := discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("first ListDiscoveredInterfaces() error = %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected f1 removed on first call, got %v discovered", len(discovered))
	}
	if _, err := os.Stat(filepath.Join(storagePath, "f1-blackholed.data")); !os.IsNotExist(err) {
		t.Fatalf("expected f1-blackholed.data removed, stat err = %v", err)
	}

	// Blackhole H2 AFTER the first call so the cached set {H1} is stale.
	if !ts.BlackholeIdentity(discoveryBlackholeHash2, nil, "test2") {
		t.Fatal("BlackholeIdentity(H2) = false, want true")
	}
	// F2: transport_id = H2 (newly blackholed, but cache is stale).
	writeDiscoveryLoadFile(t, storagePath, "f2-newly-blackholed", map[string]any{
		"transport_id": discoveryBlackholeHex2,
		"network_id":   discoveryValidNetworkIDHex,
	})

	// Second call within the TTL: cache must NOT refresh, so H2 is absent
	// from the cached set and F2 survives and loads.
	discovered, err = discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("second ListDiscoveredInterfaces() error = %v", err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected f2 to survive (stale cache, not refreshed), got %v discovered", len(discovered))
	}
	if _, err := os.Stat(filepath.Join(storagePath, "f2-newly-blackholed.data")); err != nil {
		t.Fatalf("expected f2-newly-blackholed.data to remain on disk (stale cache), stat err = %v", err)
	}

	// Simulate the 60s TTL elapsing so the next call refreshes the cache.
	now := float64(time.Now().UnixNano()) / 1e9
	discovery.blackholedUpdated = now - 61

	// Third call: cache refreshed to {H1, H2}, so F2 is removed.
	discovered, err = discovery.ListDiscoveredInterfaces(false, false)
	if err != nil {
		t.Fatalf("third ListDiscoveredInterfaces() error = %v", err)
	}
	if len(discovered) != 0 {
		t.Fatalf("expected f2 removed after cache refresh, got %v discovered", len(discovered))
	}
	if _, err := os.Stat(filepath.Join(storagePath, "f2-newly-blackholed.data")); !os.IsNotExist(err) {
		t.Fatalf("expected f2-newly-blackholed.data removed after refresh, stat err = %v", err)
	}
}
