// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestRegisterInterfaceDoesNotReannounceDestinations is the regression test for
// the announce storm. The Go port previously re-announced every local
// destination from RegisterInterface (and from a TCP client's onConnect hook
// on every reconnect). Each re-announce minted a fresh random blob, bypassing
// the configured announce interval, so any interface that re-registered or
// reconnected on a ~5s cadence flooded the network with announces every few
// seconds — a self-sustaining storm that saturated the shared transport and
// dropped link-handshake packets ("Link establishment timed out"). Python's
// add_interface (Transport.py:438-441) only appends the interface and never
// announces; this test asserts the Go port now matches that: registering an
// interface after destinations exist sends no announce. The trailing
// dest.Announce sanity check proves the send path is live, so a reintroduced
// re-announce would surface as sendCount > 0.
func TestRegisterInterfaceDoesNotReannounceDestinations(t *testing.T) {
	t.Parallel()
	testutils.RunInBubble(t, func(t *testing.T) {
		ts := NewTransportSystem(nil)
		ts.identity = mustTestNewIdentity(t, true)

		id := mustTestNewIdentity(t, true)
		// NewDestination with the real transport auto-registers the destination.
		// Standalone (not connected to a shared instance) so RegisterDestination
		// schedules no path-response announce.
		dest, err := NewDestination(ts, id, DestinationIn, DestinationSingle, "no-storm-iface")
		if err != nil {
			t.Fatalf("NewDestination: %v", err)
		}

		iface := &capturingInterface{name: "net"}
		ts.RegisterInterface(iface)

		// Settle everything before asserting the negative: any reintroduced
		// deferred re-announce would have fired by now.
		testutils.Wait()
		if iface.SendCount() != 0 {
			t.Fatalf("RegisterInterface re-announced destinations (sendCount=%v): interface registration must not mint announces", iface.SendCount())
		}

		// Sanity check: the send path is live, so a reintroduced re-announce
		// would be observable here. A direct announce reaches the registered
		// OUT interface via Outbound.
		if err := dest.Announce(nil); err != nil {
			t.Fatalf("dest.Announce sanity send failed: %v", err)
		}
		ts.WaitOutboundSends()
		if iface.SendCount() == 0 {
			t.Fatalf("dest.Announce did not reach the interface; the no-storm assertion above is not meaningful")
		}
	})
}

// TestRegisterDestinationAnnouncesPathResponseOnceWhenShared mirrors Python
// Transport.register_destination (Transport.py:2499-2517): a transport that is
// a client of a shared Reticulum instance announces a freshly-registered
// SINGLE destination exactly once as a path-response, and never re-announces
// it on interface events. A duplicate RegisterDestination call must not
// trigger a second announce.
func TestRegisterDestinationAnnouncesPathResponseOnceWhenShared(t *testing.T) {
	t.Parallel()
	testutils.RunInBubble(t, func(t *testing.T) {
		ts := NewTransportSystem(nil)
		ts.identity = mustTestNewIdentity(t, true)
		ts.SetConnectedToSharedInstance(true)

		iface := &capturingInterface{name: "local"}
		ts.RegisterInterface(iface) // no destinations yet -> no announce
		if iface.SendCount() != 0 {
			t.Fatalf("RegisterInterface sent announces before any destination existed: sendCount=%v", iface.SendCount())
		}

		id := mustTestNewIdentity(t, true)
		// NewDestination auto-registers; connectedToSharedInstance schedules one
		// path-response announce after the 250ms defer (joined to outboundWG,
		// so WaitOutboundSends advances virtual time to it and returns).
		dest, err := NewDestination(ts, id, DestinationIn, DestinationSingle, "no-storm-shared")
		if err != nil {
			t.Fatalf("NewDestination: %v", err)
		}

		ts.WaitOutboundSends()
		if iface.SendCount() != 1 {
			t.Fatalf("expected exactly one path-response announce on registration, got %v", iface.SendCount())
		}

		// A duplicate registration must not re-announce.
		ts.RegisterDestination(dest)
		ts.WaitOutboundSends()
		if iface.SendCount() != 1 {
			t.Fatalf("duplicate RegisterDestination re-announced (sendCount=%v): registration must announce at most once", iface.SendCount())
		}
	})
}

// TestOverHoppedAnnounceDoesNotInstallPath mirrors Python's PATHFINDER_M hop
// gate (Transport.py:1805-1807): an announce whose hop count has reached
// PathfinderM is not installed into the path table, rebroadcast, or delivered
// to announce handlers — it has exhausted pathfinding. go-reticulum enforces
// this in Packet.Unpack (packet.go: a raw hop count >= PathfinderM is rejected
// before dispatch), which has the same effect as Python's should_add gate
// (post-increment hops <= PathfinderM install). This test exercises the
// end-to-end Inbound path; a control announce under the limit installs a path.
func TestOverHoppedAnnounceDoesNotInstallPath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	iface := &capturingInterface{name: "net"}

	id := mustTestNewIdentity(t, true)
	// Unregistered SINGLE destination (nil transport) so the announce is
	// non-local and reaches the path-table insertion logic.
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "no-storm-hops")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// Plausible emission timebase (near local time) so the announce looks like
	// a real emission to the poison-heal plausibility gate.
	emission := uint64(time.Now().Unix())

	// Announce at the pathfinding limit: pre-increment Hops=PathfinderM (128)
	// becomes 129 after Inbound's hops++ and must be dropped.
	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, emission)
	p.Hops = PathfinderM
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	ts.Inbound(append([]byte(nil), p.Raw...), iface)

	ts.mu.Lock()
	_, dropped := ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if dropped {
		t.Fatalf("announce at PathfinderM+1 hops must not install a path table entry")
	}

	// Control: an announce under the limit installs a path.
	p2 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, emission)
	p2.Hops = 2
	if err := p2.Pack(); err != nil {
		t.Fatalf("Pack control: %v", err)
	}
	ts.Inbound(append([]byte(nil), p2.Raw...), iface)

	ts.mu.Lock()
	entry, ok := ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatalf("control announce under the hop limit should install a path table entry")
	}
	if entry.Hops != 3 { // 2 pre-increment + Inbound hops++
		t.Errorf("control path entry Hops = %v, want 3", entry.Hops)
	}
}
