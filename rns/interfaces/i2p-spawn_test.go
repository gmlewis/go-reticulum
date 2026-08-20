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

// TestI2PServerSpawnedInheritsGravityAndBurst verifies that an I2P
// server interface (I2PInterface, which reuses the TCP server's
// handleConnection spawn path) copies its gravity + the full
// ingress/egress-control burst configuration to each spawned peer at accept
// time (RNS/Interfaces/I2PInterface.py:828-840, 862: spawned_interface.gravity
// = self.gravity, v1.4.1). The propagation is wired into the shared
// spawn path; this test pins it for the I2P interface specifically so a future
// I2P-specific spawn path cannot silently drop the inheritance.
func TestI2PServerSpawnedInheritsGravityAndBurst(t *testing.T) {
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
	server, err := NewI2PInterface("i2p-server", "127.0.0.1", port, handler, onConnect)
	if err != nil {
		t.Fatalf("NewI2PInterface: %v", err)
	}
	defer func() { _ = server.Detach() }()

	i2p, ok := server.(*I2PInterface)
	if !ok {
		t.Fatalf("NewI2PInterface returned %T, want *I2PInterface", server)
	}
	if got := i2p.Type(); got != "I2PInterface" {
		t.Fatalf("I2PInterface.Type() = %q, want %q", got, "I2PInterface")
	}

	// Set a non-default gravity + every burst parameter on the parent.
	const parentGravity = 77
	i2p.TCPServerInterface.BaseInterface.SetGravity(parentGravity)
	applyNonDefaultIngressEgress(i2p.TCPServerInterface.BaseInterface)

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
		t.Fatal("spawned peer was not captured via onConnect")
	}
	if got := sp.Gravity(); got != parentGravity {
		t.Fatalf("spawned I2P peer Gravity() = %d, want %d (inherited)", got, parentGravity)
	}
	// The full ic_*/ec_pr_freq burst-param set (I2PInterface.py:828-840).
	assertIngressEgressInherited(t, sp.BaseInterface)
}
