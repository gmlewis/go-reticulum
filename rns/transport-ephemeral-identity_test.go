// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
)

// TestStartEphemeralTransportIdentityNonTransport asserts the Task 4b slice:
// a non-transport instance (enable_transport=false) initializes a fresh
// ephemeral transport identity for rebroadcast/transport-level operations
// (Python Transport.py:234-237), while the persistent identity is still
// saved to disk (stable across restarts). A transport-enabled instance uses
// the persistent identity directly, and static_transport_identity=true keeps
// the persistent identity even when transport is disabled.
func TestStartEphemeralTransportIdentityNonTransport(t *testing.T) {
	t.Parallel()

	// Case 1: non-transport instance gets an ephemeral identity that differs
	// from the persistent one saved to disk.
	dir1 := t.TempDir()
	ts1 := NewTransportSystem(nil)
	ts1.SetEnabled(false)
	if err := ts1.Start(dir1); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { ts1.Stop() })
	if ts1.identity == nil {
		t.Fatal("identity = nil after Start")
	}
	if ts1.PersistentIdentity() == nil {
		t.Fatal("persistentIdentity = nil after Start")
	}
	if bytes.Equal(ts1.identity.Hash, ts1.PersistentIdentity().Hash) {
		t.Errorf("non-transport identity should be ephemeral (distinct from persistent); both = %x", ts1.identity.Hash)
	}

	// Case 2: restarting the same storage dir loads the same persistent
	// identity but generates a NEW ephemeral one (ephemeral changes each
	// run, persistent is stable).
	dir2 := t.TempDir()
	ts2a := NewTransportSystem(nil)
	ts2a.SetEnabled(false)
	if err := ts2a.Start(dir2); err != nil {
		t.Fatalf("Start (2a): %v", err)
	}
	persistentHash2a := append([]byte(nil), ts2a.PersistentIdentity().Hash...)
	ephemeralHash2a := append([]byte(nil), ts2a.identity.Hash...)
	ts2a.Stop()

	ts2b := NewTransportSystem(nil)
	ts2b.SetEnabled(false)
	if err := ts2b.Start(dir2); err != nil {
		t.Fatalf("Start (2b): %v", err)
	}
	t.Cleanup(func() { ts2b.Stop() })
	if !bytes.Equal(ts2b.PersistentIdentity().Hash, persistentHash2a) {
		t.Errorf("persistent identity should be stable across restarts; got %x want %x", ts2b.PersistentIdentity().Hash, persistentHash2a)
	}
	if bytes.Equal(ts2b.identity.Hash, ephemeralHash2a) {
		t.Errorf("ephemeral identity should change across restarts; both = %x", ts2b.identity.Hash)
	}

	// Case 3: transport-enabled instance uses the persistent identity.
	dir3 := t.TempDir()
	ts3 := NewTransportSystem(nil)
	ts3.SetEnabled(true)
	if err := ts3.Start(dir3); err != nil {
		t.Fatalf("Start (3): %v", err)
	}
	t.Cleanup(func() { ts3.Stop() })
	if !bytes.Equal(ts3.identity.Hash, ts3.PersistentIdentity().Hash) {
		t.Errorf("transport-enabled identity should equal persistent; got %x want %x", ts3.identity.Hash, ts3.PersistentIdentity().Hash)
	}

	// Case 4: static_transport_identity=true with transport disabled keeps
	// the persistent identity (no ephemeral).
	dir4 := t.TempDir()
	ts4 := NewTransportSystem(nil)
	ts4.SetEnabled(false)
	ts4.SetStaticTransportIdentity(true)
	if err := ts4.Start(dir4); err != nil {
		t.Fatalf("Start (4): %v", err)
	}
	t.Cleanup(func() { ts4.Stop() })
	if !bytes.Equal(ts4.identity.Hash, ts4.PersistentIdentity().Hash) {
		t.Errorf("static_transport_identity should keep persistent identity; got %x want %x", ts4.identity.Hash, ts4.PersistentIdentity().Hash)
	}
}
