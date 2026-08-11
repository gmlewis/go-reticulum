// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// TestTCPServerSpawnedInheritsGravity verifies that a TCP server copies its
// Gravity to each spawned client at accept time (RNS/Interfaces/TCPInterface.py
// :639, v1.4.1: `spawned_interface.gravity = self.gravity`). The same spawn
// path backs I2P and Backbone servers (they reuse newTCPServerInterface), so
// this also covers those.
func TestTCPServerSpawnedInheritsGravity(t *testing.T) {
	t.Parallel()
	port := reserveTCPPort(t)

	var mu sync.Mutex
	var spawned *TCPClientInterface
	handler := func(data []byte, _ Interface) {}
	onConnect := func(iface Interface) {
		mu.Lock()
		defer mu.Unlock()
		if ci, ok := iface.(*TCPClientInterface); ok {
			spawned = ci
		}
	}
	server, err := NewTCPServerInterface("gravity-server", "127.0.0.1", port, handler, onConnect)
	if err != nil {
		t.Fatalf("NewTCPServerInterface: %v", err)
	}
	defer func() { _ = server.Detach() }()

	const parentGravity = 42
	server.SetGravity(parentGravity)
	if got := server.Gravity(); got != parentGravity {
		t.Fatalf("server Gravity() = %d, want %d", got, parentGravity)
	}

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		sp := spawned
		mu.Unlock()
		if sp != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	sp := spawned
	mu.Unlock()
	if sp == nil {
		t.Fatal("spawned TCPClientInterface was not captured via onConnect")
	}
	if got := sp.Gravity(); got != parentGravity {
		t.Fatalf("spawned client Gravity() = %d, want %d (inherited from parent)", got, parentGravity)
	}
}

// TestAutoSpawnedPeerInheritsGravity verifies that an AutoInterface copies its
// Gravity to each spawned peer (RNS/Interfaces/AutoInterface.py:583, v1.4.1:
// `spawned_interface.gravity = self.gravity`).
func TestAutoSpawnedPeerInheritsGravity(t *testing.T) {
	t.Parallel()

	auto, err := NewAutoInterface("gravity-auto", AutoInterfaceConfig{}, func(data []byte, _ Interface) {}, func(_ Interface) {})
	if err != nil {
		t.Fatalf("NewAutoInterface: %v", err)
	}
	defer func() { _ = auto.Detach() }()

	const parentGravity = 7
	auto.SetGravity(parentGravity)
	if got := auto.Gravity(); got != parentGravity {
		t.Fatalf("auto Gravity() = %d, want %d", got, parentGravity)
	}

	// Drive the real peer-adoption path the discovery loop uses, then read
	// back the spawned peer and assert it inherited the parent's gravity.
	auto.addPeer("fe80::1%lo0", "lo0")
	auto.mu.Lock()
	peer := auto.spawnedInterfaces["fe80::1%lo0"]
	auto.mu.Unlock()
	if peer == nil {
		t.Fatal("addPeer did not create a spawned peer")
	}
	if got := peer.Gravity(); got != parentGravity {
		t.Fatalf("spawned auto peer Gravity() = %d, want %d (inherited from parent)", got, parentGravity)
	}
}
