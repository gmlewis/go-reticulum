// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync/atomic"
	"testing"
	"time"
)

// panicOnHashInterface is a minimal interfaces.Interface whose Name() panics,
// so the synthesis body's interfaceHash/iface.Name() path raises a panic that
// the deferred recover must absorb.
type panicOnHashInterface struct {
	dummyInterface
	called atomic.Int32
}

func (p *panicOnHashInterface) Name() string {
	p.called.Add(1)
	panic("synthesis forced panic")
}

// TestSynthesizeTunnelLogsMissingIdentityAndKeepsRunning verifies that
// when the transport has no identity (enable_transport is False), the
// synthesis body fails early, logs the error, and returns without panicking
// so the transport stays usable (Python Transport.synthesize_tunnel except
// clause, Transport.py:2417).
func TestSynthesizeTunnelLogsMissingIdentityAndKeepsRunning(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	// No transport identity: SynthesizeTunnel must take the error path.
	iface := &dummyInterface{name: "no-id-iface"}

	completed := completesWithin(func() { ts.SynthesizeTunnel(iface) }, time.Second)
	if !completed {
		t.Fatal("SynthesizeTunnel did not return promptly with a missing identity")
	}

	// The transport must still be usable after the failed synthesis: e.g.
	// registering an interface and querying it must work.
	ts.RegisterInterface(&dummyInterface{name: "after"})
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("transport unusable after failed synthesis: GetInterfaces=%d, want 1", got)
	}
}

// TestSynthesizeTunnelRecoversFromPanicAndKeepsRunning verifies that
// a panic inside the synthesis body is caught by the deferred recover, logged,
// and does not propagate, so the transport keeps running (Python's broad
// except Exception clause guards the whole body).
func TestSynthesizeTunnelRecoversFromPanicAndKeepsRunning(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	id := mustTestNewIdentity(t, true)
	ts.identity = id

	iface := &panicOnHashInterface{dummyInterface: dummyInterface{name: "panic-iface"}}

	completed := completesWithin(func() { ts.SynthesizeTunnel(iface) }, time.Second)
	if !completed {
		t.Fatal("SynthesizeTunnel did not return promptly after a forced panic")
	}
	if iface.called.Load() == 0 {
		t.Fatal("synthesis body did not reach the panicking interface call")
	}

	// The transport must still be usable after the recovered panic.
	ts.RegisterInterface(&dummyInterface{name: "after"})
	if got := len(ts.GetInterfaces()); got != 1 {
		t.Fatalf("transport unusable after recovered panic: GetInterfaces=%d, want 1", got)
	}
}

// TestSynthesizeTunnelWithIdentitySendsPacket verifies that with a
// transport identity present, the synthesis body builds the establishment
// payload and dispatches it via Outbound on the attached interface. A
// capturing interface confirms a packet was actually sent (Python
// Transport.py:2407-2415 packet.send()).
func TestSynthesizeTunnelWithIdentitySendsPacket(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(mustTestLogger(t, LogDebug))
	id := mustTestNewIdentity(t, true)
	ts.identity = id

	iface := &capturingInterface{name: "send-iface"}
	ts.RegisterInterface(iface)

	completesWithin(func() { ts.SynthesizeTunnel(iface) }, time.Second)

	if iface.sendCount == 0 {
		t.Fatal("SynthesizeTunnel did not send an establishment packet on the interface")
	}
}
