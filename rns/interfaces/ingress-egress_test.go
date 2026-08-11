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

// applyNonDefaultIngressEgress sets every ingress/egress-control field on bi
// to a value distinct from the Interface.__init__ defaults, returning the
// expected golden values in the same order the assertions check them.
func applyNonDefaultIngressEgress(bi *BaseInterface) {
	bi.SetIngressControl(false)
	bi.SetICMaxHeldAnnounces(99)
	bi.SetICBurstHold(7.5)
	bi.SetICBurstFreqNew(1.5)
	bi.SetICBurstFreq(20)
	bi.SetICPrBurstFreqNew(2.5)
	bi.SetICPrBurstFreq(9)
	bi.SetICNewTime(100)
	bi.SetICBurstPenalty(12)
	bi.SetICHeldReleaseInterval(3)
	bi.SetECPrFreq(8)
	bi.SetEgressControl(true)
}

// assertIngressEgressInherited verifies the spawned peer copied every
// ingress/egress-control field from its parent (Python spawn blocks:
// TCPInterface.py:595-608, AutoInterface.py:542-554, I2PInterface.py:828-840,
// BackboneInterface.py:446-458, v1.1.5).
func assertIngressEgressInherited(t *testing.T, got *BaseInterface) {
	t.Helper()
	if got.IngressControl() != false {
		t.Errorf("spawned IngressControl = %v, want false", got.IngressControl())
	}
	if got.ICMaxHeldAnnounces() != 99 {
		t.Errorf("spawned ICMaxHeldAnnounces = %v, want 99", got.ICMaxHeldAnnounces())
	}
	if got.ICBurstHold() != 7.5 {
		t.Errorf("spawned ICBurstHold = %v, want 7.5", got.ICBurstHold())
	}
	if got.ICBurstFreqNew() != 1.5 {
		t.Errorf("spawned ICBurstFreqNew = %v, want 1.5", got.ICBurstFreqNew())
	}
	if got.ICBurstFreq() != 20 {
		t.Errorf("spawned ICBurstFreq = %v, want 20", got.ICBurstFreq())
	}
	if got.ICPrBurstFreqNew() != 2.5 {
		t.Errorf("spawned ICPrBurstFreqNew = %v, want 2.5", got.ICPrBurstFreqNew())
	}
	if got.ICPrBurstFreq() != 9 {
		t.Errorf("spawned ICPrBurstFreq = %v, want 9", got.ICPrBurstFreq())
	}
	if got.ICNewTime() != 100 {
		t.Errorf("spawned ICNewTime = %v, want 100", got.ICNewTime())
	}
	if got.ICBurstPenalty() != 12 {
		t.Errorf("spawned ICBurstPenalty = %v, want 12", got.ICBurstPenalty())
	}
	if got.ICHeldReleaseInterval() != 3 {
		t.Errorf("spawned ICHeldReleaseInterval = %v, want 3", got.ICHeldReleaseInterval())
	}
	if got.ECPrFreq() != 8 {
		t.Errorf("spawned ECPrFreq = %v, want 8", got.ECPrFreq())
	}
	if got.EgressControl() != true {
		t.Errorf("spawned EgressControl = %v, want true", got.EgressControl())
	}
}

// TestTCPServerSpawnedInheritsIngressEgress verifies the TCP server copies
// its full ingress/egress-control configuration to each spawned client at
// accept time (RNS/Interfaces/TCPInterface.py:595-608, v1.1.5). The same
// handleConnection spawn path backs I2P and Backbone servers (they reuse
// newTCPServerInterface), so this also covers those.
func TestTCPServerSpawnedInheritsIngressEgress(t *testing.T) {
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
	server, err := NewTCPServerInterface("ic-server", "127.0.0.1", port, handler, onConnect)
	if err != nil {
		t.Fatalf("NewTCPServerInterface: %v", err)
	}
	defer func() { _ = server.Detach() }()

	applyNonDefaultIngressEgress(server.BaseInterface)

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
	assertIngressEgressInherited(t, sp.BaseInterface)
}

// TestAutoSpawnedPeerInheritsIngressEgress verifies the AutoInterface copies
// its full ingress/egress-control configuration to each spawned peer
// (RNS/Interfaces/AutoInterface.py:542-554, v1.1.5).
func TestAutoSpawnedPeerInheritsIngressEgress(t *testing.T) {
	t.Parallel()

	auto, err := NewAutoInterface("ic-auto", AutoInterfaceConfig{}, func(data []byte, _ Interface) {}, func(_ Interface) {})
	if err != nil {
		t.Fatalf("NewAutoInterface: %v", err)
	}
	defer func() { _ = auto.Detach() }()

	applyNonDefaultIngressEgress(auto.BaseInterface)

	auto.addPeer("fe80::1%lo0", "lo0")
	auto.mu.Lock()
	peer := auto.spawnedInterfaces["fe80::1%lo0"]
	auto.mu.Unlock()
	if peer == nil {
		t.Fatal("addPeer did not create a spawned peer")
	}
	assertIngressEgressInherited(t, peer.BaseInterface)
}
