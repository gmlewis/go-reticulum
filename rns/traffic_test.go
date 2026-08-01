// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// trafficTestInterface is a controllable Interface with settable cumulative
// byte counters and speed slots, used to drive the traffic-counter loop
// deterministically. It mirrors the fields Python reads/writes on a real
// interface (rxb, txb, current_rx_speed, current_tx_speed).
type trafficTestInterface struct {
	dummyInterface
	rxb, txb          uint64
	rxSpeed, txSpeed  float64
	speedSetCallCount int
}

func (t *trafficTestInterface) BytesReceived() uint64 { return t.rxb }
func (t *trafficTestInterface) BytesSent() uint64     { return t.txb }
func (t *trafficTestInterface) SetTrafficSpeeds(rx, tx float64) {
	t.rxSpeed = rx
	t.txSpeed = tx
	t.speedSetCallCount++
}
func (t *trafficTestInterface) CurrentRxSpeed() float64 { return t.rxSpeed }
func (t *trafficTestInterface) CurrentTxSpeed() float64 { return t.txSpeed }

// trafficChildInterface wraps trafficTestInterface and reports a parent,
// mirroring Python interfaces with parent_interface set (tunnel
// sub-interfaces, RNode multi children) which count_traffic_loop skips.
type trafficChildInterface struct {
	trafficTestInterface
	parent interfaces.Interface
}

func (t *trafficChildInterface) ParentInterface() interfaces.Interface { return t.parent }

// TestCountTrafficPassGolden captures the exact speed/byte math of Python
// Transport.count_traffic_loop (Transport.py:419-451) with a controlled clock
// and known byte deltas. The formula is crxs = (rx_diff*8)/ts_diff (bits per
// second); aggregates are traffic_rxb/txb (cumulative per-interval deltas) and
// speed_rx/tx (current sum of per-interface speeds).
func TestCountTrafficPassGolden(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ifaceA := &trafficTestInterface{dummyInterface: dummyInterface{name: "A"}}
	ifaceB := &trafficTestInterface{dummyInterface: dummyInterface{name: "B"}}
	ifaceA.rxb, ifaceA.txb = 1000, 200
	ifaceB.rxb, ifaceB.txb = 500, 50
	ts.RegisterInterface(ifaceA)
	ts.RegisterInterface(ifaceB)

	t0 := time.Unix(1_700_000_000, 0)

	// First pass initializes per-interface counters and produces no speed,
	// no byte deltas (Python: the else-branch sets transport_traffic_counter
	// and contributes nothing to rxb/rxs this pass).
	rxb, txb, rxs, txs := ts.countTrafficPass(t0)
	if rxb != 0 || txb != 0 || rxs != 0 || txs != 0 {
		t.Fatalf("first pass: rxb=%v txb=%v rxs=%v txs=%v, all want 0", rxb, txb, rxs, txs)
	}
	if got := ts.TrafficRxb(); got != 0 {
		t.Fatalf("after first pass TrafficRxb=%v, want 0", got)
	}
	if ifaceA.speedSetCallCount != 0 || ifaceB.speedSetCallCount != 0 {
		t.Fatalf("first pass must not set speeds: A=%v B=%v", ifaceA.speedSetCallCount, ifaceB.speedSetCallCount)
	}

	// Advance byte counters by known deltas over a 1s interval.
	ifaceA.rxb, ifaceA.txb = 3000, 400 // deltas: rx=2000, tx=200
	ifaceB.rxb, ifaceB.txb = 1500, 150 // deltas: rx=1000, tx=100

	rxb, txb, rxs, txs = ts.countTrafficPass(t0.Add(1 * time.Second))
	// A: crxs=(2000*8)/1=16000, ctxs=(200*8)/1=1600
	// B: crxs=(1000*8)/1=8000,  ctxs=(100*8)/1=800
	wantRXB, wantTXB := uint64(3000), uint64(300)
	wantRXS, wantTXS := 16000.0+8000.0, 1600.0+800.0
	if rxb != wantRXB || txb != wantTXB {
		t.Fatalf("second pass deltas: rxb=%v want %v, txb=%v want %v", rxb, wantRXB, txb, wantTXB)
	}
	if rxs != wantRXS || txs != wantTXS {
		t.Fatalf("second pass speeds: rxs=%v want %v, txs=%v want %v", rxs, wantRXS, txs, wantTXS)
	}
	if got := ts.TrafficRxb(); got != wantRXB {
		t.Fatalf("after second pass TrafficRxb=%v, want %v", got, wantRXB)
	}
	if got := ts.TrafficTxb(); got != wantTXB {
		t.Fatalf("after second pass TrafficTxb=%v, want %v", got, wantTXB)
	}
	if got := ts.SpeedRx(); got != wantRXS {
		t.Fatalf("after second pass SpeedRx=%v, want %v", got, wantRXS)
	}
	if got := ts.SpeedTx(); got != wantTXS {
		t.Fatalf("after second pass SpeedTx=%v, want %v", got, wantTXS)
	}
	if ifaceA.CurrentRxSpeed() != 16000 || ifaceA.CurrentTxSpeed() != 1600 {
		t.Fatalf("ifaceA speeds: rx=%v want 16000, tx=%v want 1600", ifaceA.CurrentRxSpeed(), ifaceA.CurrentTxSpeed())
	}
	if ifaceB.CurrentRxSpeed() != 8000 || ifaceB.CurrentTxSpeed() != 800 {
		t.Fatalf("ifaceB speeds: rx=%v want 8000, tx=%v want 800", ifaceB.CurrentRxSpeed(), ifaceB.CurrentTxSpeed())
	}

	// Third pass with no byte changes over 1s: deltas are 0, so speeds drop
	// to 0 and traffic_rxb/txb do not grow.
	rxb, txb, rxs, txs = ts.countTrafficPass(t0.Add(2 * time.Second))
	if rxb != 0 || txb != 0 || rxs != 0 || txs != 0 {
		t.Fatalf("third pass: rxb=%v txb=%v rxs=%v txs=%v, all want 0", rxb, txb, rxs, txs)
	}
	if got := ts.TrafficRxb(); got != wantRXB {
		t.Fatalf("after third pass TrafficRxb=%v, want %v (unchanged)", got, wantRXB)
	}
	if got := ts.SpeedRx(); got != 0 {
		t.Fatalf("after third pass SpeedRx=%v, want 0", got)
	}
}

// TestCountTrafficPassSkipsParentedInterfaces verifies the
// parent_interface == None guard (Transport.py:426): a child interface that
// reports a parent is not counted toward traffic_rxb/txb and is not given
// speeds.
func TestCountTrafficPassSkipsParentedInterfaces(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	parent := &trafficTestInterface{dummyInterface: dummyInterface{name: "parent"}}
	child := &trafficChildInterface{
		trafficTestInterface: trafficTestInterface{dummyInterface: dummyInterface{name: "child"}},
		parent:               parent,
	}
	ts.RegisterInterface(parent)
	ts.RegisterInterface(child)

	t0 := time.Unix(1_700_000_000, 0)
	ts.countTrafficPass(t0) // init counters for both

	// Advance only the child's counters; the parent stays flat. Because the
	// child is skipped, no bytes accrue and the child gets no speed.
	child.rxb, child.txb = 5000, 1000
	rxb, txb, rxs, txs := ts.countTrafficPass(t0.Add(1 * time.Second))
	if rxb != 0 || txb != 0 || rxs != 0 || txs != 0 {
		t.Fatalf("parented child contributed: rxb=%v txb=%v rxs=%v txs=%v, all want 0", rxb, txb, rxs, txs)
	}
	if child.speedSetCallCount != 0 {
		t.Fatalf("parented child had speeds set: %v, want 0", child.speedSetCallCount)
	}
}

// TestInterfaceStatsSpeeds wires the traffic loop's output into the
// interface_stats RPC (Phase G.2): after driving countTrafficPass, the RPC's
// per-interface rxs/txs reflect current_rx_speed/current_tx_speed and the
// aggregate rxb/txb/rxs/txs reflect traffic_rxb/traffic_txb/speed_rx/speed_tx
// (matching Python reticulum.py:1191-1252), with no hardcoded 0s.
func TestInterfaceStatsSpeeds(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	iface := &trafficTestInterface{dummyInterface: dummyInterface{name: "speeds-iface"}}
	ts.RegisterInterface(iface)

	t0 := time.Unix(1_700_000_000, 0)
	ts.countTrafficPass(t0)         // init counter
	iface.rxb, iface.txb = 800, 200 // deltas over 1s: rx=800, tx=200
	ts.countTrafficPass(t0.Add(1 * time.Second))
	// crxs=(800*8)/1=6400, ctxs=(200*8)/1=1600
	wantRXS, wantTXS := 6400.0, 1600.0
	wantRXB, wantTXB := uint64(800), uint64(200)

	r := &Reticulum{transport: ts}
	stats := r.getInterfaceStats()

	if got := stats["rxs"]; got != wantRXS {
		t.Fatalf("aggregate rxs=%v, want %v", got, wantRXS)
	}
	if got := stats["txs"]; got != wantTXS {
		t.Fatalf("aggregate txs=%v, want %v", got, wantTXS)
	}
	if got := stats["rxb"]; got != wantRXB {
		t.Fatalf("aggregate rxb=%v, want %v (traffic_rxb)", got, wantRXB)
	}
	if got := stats["txb"]; got != wantTXB {
		t.Fatalf("aggregate txb=%v, want %v (traffic_txb)", got, wantTXB)
	}

	entries := stats["interfaces"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected 1 interface entry, got %d", len(entries))
	}
	entry := entries[0].(map[string]any)
	if got := entry["rxs"]; got != wantRXS {
		t.Fatalf("per-interface rxs=%v, want %v", got, wantRXS)
	}
	if got := entry["txs"]; got != wantTXS {
		t.Fatalf("per-interface txs=%v, want %v", got, wantTXS)
	}
}
