// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// TestLinkReceiveRevivesStale covers Python Link.receive (Link.py:939):
// `if self.status == Link.STALE: self.status = Link.ACTIVE`. When the
// watchdog marks a link STALE it sleeps one stale-grace window before
// tearing it down, and a packet arriving during that window flips the link
// back to ACTIVE. Without the revival a link whose keepalive echo arrived
// a few seconds late (the loopback echo-skip knife edge) is torn down by
// the next watchdog step even though traffic resumed during the grace
// period, making Go links strictly more fragile than the Python stack.
//
// The revival is gated on the same guard as the rx counter (Python
// Link.py:931): a CLOSED link stays closed, and the initiator's own 0xFF
// keepalive echo (looped back on a shared interface) revives nothing.
func TestLinkReceiveRevivesStale(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	peerID := mustTestNewIdentity(t, true)
	peerDest := mustTestNewDestination(t, ts, peerID, DestinationIn, DestinationSingle, "peer")
	link := mustTestNewLink(t, ts, peerDest)
	iface := &capturingInterface{name: "capture"}

	link.initiator = false
	link.status.Store(LinkStale)
	link.linkID = []byte("stale_revival_link")
	link.hash = link.linkID
	link.attachedInterface = iface
	link.keepalive = 5 * time.Second
	now := time.Unix(1700000000, 0)
	link.now = func() time.Time { return now }

	// A regular inbound packet during the stale-grace window revives the link.
	link.receive(&Packet{Context: ContextKeepalive, Data: []byte{0xFE}})
	if got := link.status.Load(); got != LinkActive {
		t.Fatalf("link status=%v after inbound packet during stale grace, want LinkActive (Python Link.py:939 revival)", got)
	}

	// A CLOSED link never revives, mirroring the Python guard.
	link.status.Store(LinkClosed)
	link.receive(&Packet{Context: ContextKeepalive, Data: []byte{0xFE}})
	if got := link.status.Load(); got != LinkClosed {
		t.Fatalf("link status=%v after inbound packet on a closed link, want LinkClosed", got)
	}

	// The initiator's own 0xFF keepalive echo does not count (Python Link.py:931)
	// and therefore revives nothing.
	link.status.Store(LinkStale)
	link.initiator = true
	link.receive(&Packet{Context: ContextKeepalive, Data: []byte{0xFF}})
	if got := link.status.Load(); got != LinkStale {
		t.Fatalf("link status=%v after initiator echo on a stale link, want LinkStale (echo must not revive)", got)
	}

	// A non-keepalive packet from the initiator still counts and revives.
	link.receive(&Packet{Context: ContextNone, Data: []byte("payload")})
	if got := link.status.Load(); got != LinkActive {
		t.Fatalf("link status=%v after initiator payload on a stale link, want LinkActive", got)
	}
}
