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

// TestAnnounceRebroadcastGateTransportDisabled verifies that a standalone
// node with transport DISABLED does NOT queue a received announce for
// rebroadcast, matching Python RNS Transport.py:1948:
//
//	if (RNS.Reticulum.transport_enabled() or is_from_local_client) and
//	   packet.context != RNS.Packet.PATH_RESPONSE:
//	    # Insert announce into announce table for retransmission
//
// The Go port previously gated rebroadcast on !connectedToSharedInstance
// instead of ts.Enabled() || isFromLocalClient. A standalone node with
// enable_transport=False had connectedToSharedInstance=false, so the Go
// code rebroadcast announces using an ephemeral transport identity even
// though transport was disabled. Other nodes receiving this rebroadcast
// learned paths pointing to the ephemeral identity — paths that could never
// be used because the non-transport node does not forward transport-id-
// addressed packets (Inbound's transport-handling block is gated on
// ts.Enabled()). This is a root cause of "No path to destination known":
// the node knows the destination (from the announce handler) but the
// learned path points to a non-functional ephemeral next-hop.
func TestAnnounceRebroadcastGateTransportDisabled(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(false) // transport disabled (enable_transport = False)

	iface := &capturingInterface{name: "rx-disabled", gravity: 0}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "rebroadcast-gate-disabled")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 42)
	p.Hops = 0
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	raw := append([]byte(nil), p.Raw...)

	ts.Inbound(raw, iface)

	// Path should still be learned (path learning is NOT gated on
	// transport_enabled — Python learns paths from all received announces).
	if !ts.HasPath(dest.Hash) {
		t.Fatal("expected path to be learned even with transport disabled")
	}

	// Announce should NOT be queued for rebroadcast when transport is
	// disabled and the announce is not from a local client.
	ts.mu.Lock()
	_, queued := ts.announceTable[string(dest.Hash)]
	ts.mu.Unlock()
	if queued {
		t.Error("announce was queued for rebroadcast with transport disabled; " +
			"Python does not rebroadcast when transport_enabled=False and " +
			"!is_from_local_client (Transport.py:1948)")
	}
}

// TestAnnounceRebroadcastGateTransportEnabled verifies that a node with
// transport ENABLED DOES queue a received announce for rebroadcast,
// matching Python behavior.
func TestAnnounceRebroadcastGateTransportEnabled(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true) // transport enabled (enable_transport = True)

	iface := &capturingInterface{name: "rx-enabled", gravity: 0}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "rebroadcast-gate-enabled")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 42)
	p.Hops = 0
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	raw := append([]byte(nil), p.Raw...)

	ts.Inbound(raw, iface)

	if !ts.HasPath(dest.Hash) {
		t.Fatal("expected path to be learned with transport enabled")
	}

	ts.mu.Lock()
	_, queued := ts.announceTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !queued {
		t.Error("announce should be queued for rebroadcast when transport is enabled")
	}
}

// TestAnnounceRebroadcastGatePathResponseNotRebroadcast verifies that a path
// response announce is NEVER queued for rebroadcast, regardless of
// transport_enabled, matching Python's
// `packet.context != RNS.Packet.PATH_RESPONSE` gate.
func TestAnnounceRebroadcastGatePathResponseNotRebroadcast(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true) // even with transport enabled

	iface := &capturingInterface{name: "rx-pr", gravity: 0}

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "pr-no-rebroadcast")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 42)
	p.Hops = 0
	p.Context = ContextPathResponse
	if err := p.Pack(); err != nil {
		t.Fatalf("Pack: %v", err)
	}
	raw := append([]byte(nil), p.Raw...)

	ts.Inbound(raw, iface)

	// Path should still be learned from a path response.
	if !ts.HasPath(dest.Hash) {
		t.Fatal("expected path to be learned from path response")
	}

	// Path response should NOT be queued for rebroadcast.
	ts.mu.Lock()
	_, queued := ts.announceTable[string(dest.Hash)]
	ts.mu.Unlock()
	if queued {
		t.Error("path response announce was queued for rebroadcast; " +
			"Python never rebroadcasts PATH_RESPONSE announces")
	}
}

// TestAnnouncePathExpiryInterfaceMode verifies that the path-table expiry
// for a learned path depends on the receiving interface's mode, matching
// Python Transport.py:1932-1939:
//
//	if   interface.mode == MODE_ACCESS_POINT: expires = now + AP_PATH_TIME (1 day)
//	elif interface.mode == MODE_ROAMING:      expires = now + ROAMING_PATH_TIME (6h)
//	else:                                    expires = now + PATHFINDER_E (1 week)
//
// The Go port previously used a fixed 1-week expiry for all modes, so paths
// learned from Access-Point and Roaming interfaces persisted up to 168x
// longer than Python intended, causing stale unreachable paths to clog the
// path table long after the mobile/ephemeral peer had departed.
func TestAnnouncePathExpiryInterfaceMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mode    int
		wantDur time.Duration
	}{
		{"full", interfaces.ModeFull, pathfinderE},
		{"access_point", interfaces.ModeAccessPoint, apPathTime},
		{"roaming", interfaces.ModeRoaming, roamingPathTime},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := NewTransportSystem(nil)
			ts.identity = mustTestNewIdentity(t, true)

			iface := &modeInterface{capturingInterface: capturingInterface{name: "rx-" + tc.name}, modeVal: tc.mode}

			id := mustTestNewIdentity(t, true)
			dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "expiry-"+tc.name)
			if err != nil {
				t.Fatalf("NewDestination: %v", err)
			}

			p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 42)
			p.Hops = 0
			if err := p.Pack(); err != nil {
				t.Fatalf("Pack: %v", err)
			}
			raw := append([]byte(nil), p.Raw...)

			ts.Inbound(raw, iface)

			ts.mu.Lock()
			entry, ok := ts.pathTable[string(dest.Hash)]
			ts.mu.Unlock()
			if !ok {
				t.Fatal("expected path table entry")
			}

			gotDur := entry.Expires.Sub(entry.Timestamp)
			// Allow a small tolerance for the time.Now() calls in Timestamp
			// and Expires (they use separate time.Now() calls).
			tolerance := 2 * time.Second
			if gotDur < tc.wantDur-tolerance || gotDur > tc.wantDur+tolerance {
				t.Errorf("path expiry duration = %v, want ~%v (mode=%v)",
					gotDur, tc.wantDur, tc.mode)
			}
		})
	}
}

// modeInterface is a capturingInterface with an overridable mode.
type modeInterface struct {
	capturingInterface
	modeVal int
}

func (m *modeInterface) Mode() int { return m.modeVal }
