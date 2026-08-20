// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// readyWaitDest builds a registered PLAIN IN destination whose packet callback
// counts deliveries, plus a packed packet addressed to it. PLAIN destinations
// carry no keys, so the packet round-trips through Pack/Unpack unchanged.
func readyWaitDest(t *testing.T, ts *TransportSystem, aspect string, payload string) (*Destination, []byte, *atomic.Int32) {
	t.Helper()
	dest := mustTestNewDestination(t, ts, nil, DestinationIn, DestinationPlain, "testapp", aspect)
	ts.RegisterDestination(dest)
	var got atomic.Int32
	dest.SetPacketCallback(func(data []byte, p *Packet) { got.Add(1) })
	p := NewPacket(dest, []byte(payload))
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	return dest, p.Raw, &got
}

// TestInboundWaitsForReadyDuringStart verifies that Inbound must hold
// a packet that arrives while Start is in progress (running=true, ready=false)
// and only process it once Start completes and the ready flag is set, matching
// Python Transport.inbound's READY_WAIT loop (Transport.py:1430-1437). The
// packet is NOT delivered while ready is false, then IS delivered once ready
// becomes true.
func TestInboundWaitsForReadyDuringStart(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	_, raw, got := readyWaitDest(t, ts, "readywait", "hello-ready")
	iface := &dummyInterface{name: "dummy"}

	// Simulate "Start in progress": running=true, ready=false. Shorten the
	// poll interval and timeout so the test runs fast.
	ts.mu.Lock()
	ts.running = true
	ts.ready = false
	ts.readyPollInterval = 10 * time.Millisecond
	ts.readyWaitTimeout = 500 * time.Millisecond
	ts.mu.Unlock()

	done := make(chan struct{})
	go func() {
		ts.Inbound(raw, iface)
		close(done)
	}()

	// While ready is false, Inbound must block: the callback has not fired.
	time.Sleep(60 * time.Millisecond)
	if got.Load() != 0 {
		t.Fatalf("packet processed before ready (got=%v, want 0)", got.Load())
	}

	// Start completes -> ready=true. Inbound must now process the packet.
	ts.mu.Lock()
	ts.ready = true
	ts.mu.Unlock()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Inbound did not return after ready was set")
	}
	if got.Load() != 1 {
		t.Fatalf("packet not processed after ready (got=%v, want 1)", got.Load())
	}

	ts.mu.Lock()
	ts.running = false
	ts.ready = false
	ts.mu.Unlock()
}

// TestInboundDropsOnReadyTimeout verifies the drop path: if the
// transport never becomes ready within readyWaitTimeout, Inbound logs a
// warning and drops the packet instead of blocking forever (Python
// Transport.py:1435-1437).
func TestInboundDropsOnReadyTimeout(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	_, raw, got := readyWaitDest(t, ts, "readydrop", "hello-drop")
	iface := &dummyInterface{name: "dummy"}

	ts.mu.Lock()
	ts.running = true
	ts.ready = false
	ts.readyPollInterval = 10 * time.Millisecond
	ts.readyWaitTimeout = 80 * time.Millisecond
	ts.mu.Unlock()

	start := time.Now()
	ts.Inbound(raw, iface) // never becomes ready -> times out and drops
	elapsed := time.Since(start)

	if got.Load() != 0 {
		t.Fatalf("packet processed on ready timeout (got=%v, want 0)", got.Load())
	}
	if elapsed < ts.readyWaitTimeout {
		t.Fatalf("Inbound returned %v before readyWaitTimeout %v", elapsed, ts.readyWaitTimeout)
	}

	ts.mu.Lock()
	ts.running = false
	ts.ready = false
	ts.mu.Unlock()
}

// TestInboundProcessesImmediatelyWhenNotStarted asserts the READY_WAIT gate
// only applies during the startup window. A transport that was never Started
// (running=false, as used throughout the loopback test harness) must process
// packets immediately — the wait is conditioned on running && !ready so the
// non-started path is unaffected.
func TestInboundProcessesImmediatelyWhenNotStarted(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil) // never Started: running=false
	_, raw, got := readyWaitDest(t, ts, "notstarted", "hello-immediate")
	iface := &dummyInterface{name: "dummy"}

	ts.Inbound(raw, iface)
	if got.Load() != 1 {
		t.Fatalf("non-started transport did not process packet (got=%v, want 1)", got.Load())
	}
}

// TestStartSetsReadyFlag confirms a real Start sets the ready flag at the end,
// so a subsequent Inbound on the started transport does not wait.
func TestStartSetsReadyFlag(t *testing.T) {
	t.Parallel()

	ts := newTestTransportSystem(t)
	ts.readyPollInterval = 10 * time.Millisecond
	ts.readyWaitTimeout = 200 * time.Millisecond

	ts.mu.Lock()
	if ts.ready {
		t.Fatal("ready=true before Start")
	}
	ts.mu.Unlock()

	if err := ts.Start(testutils.TempDir(t, tempDirPrefix)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(ts.Stop)

	ts.mu.Lock()
	ready := ts.ready
	ts.mu.Unlock()
	if !ready {
		t.Fatal("ready=false after Start completed")
	}

	_, raw, got := readyWaitDest(t, ts, "startflag", "hello-after-start")
	ts.Inbound(raw, &dummyInterface{name: "dummy"})
	if got.Load() != 1 {
		t.Fatalf("started transport did not process packet (got=%v, want 1)", got.Load())
	}
}
