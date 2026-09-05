// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"
)

// TestLinkReceiveIgnoresInitiatorKeepaliveEcho pins Python Link.py:930-939:
// the initiator's own 0xFF keepalive echo (looped back over a shared medium)
// must neither count toward rx nor refresh last_inbound. Without the clock
// guard an echo of a DEAD link's keepalive — relayed back by any path — keeps
// refreshing last_inbound and the link's watchdog never fires, leaving the
// client "Connected" to a hub that no longer holds the session.
func TestLinkReceiveIgnoresInitiatorKeepaliveEcho(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	peerID := mustTestNewIdentity(t, true)
	peerDest := mustTestNewDestination(t, ts, peerID, DestinationIn, DestinationSingle, "peer")
	link := mustTestNewLink(t, ts, peerDest)
	iface := &capturingInterface{name: "capture"}

	link.initiator = true
	link.status.Store(LinkActive)
	link.linkID = []byte("echo_guard_link")
	link.hash = link.linkID
	link.attachedInterface = iface

	link.mu.Lock()
	before := link.lastInbound
	link.mu.Unlock()

	// The initiator's own 0xFF echo must not refresh the watchdog clock.
	for range 5 {
		link.receive(&Packet{Context: ContextKeepalive, Data: []byte{0xFF}, ReceivingInterface: iface})
	}
	link.mu.Lock()
	afterEcho := link.lastInbound
	link.mu.Unlock()
	if !afterEcho.Equal(before) {
		t.Fatalf("lastInbound advanced on the initiator's own keepalive echo: %v -> %v", before, afterEcho)
	}

	// The hub's 0xFE reply is real inbound traffic and must refresh it.
	time.Sleep(2 * time.Millisecond)
	link.receive(&Packet{Context: ContextKeepalive, Data: []byte{0xFE}, ReceivingInterface: iface})
	link.mu.Lock()
	afterReply := link.lastInbound
	link.mu.Unlock()
	if !afterReply.After(before) {
		t.Fatalf("lastInbound did not advance on the hub's keepalive reply: %v", afterReply)
	}
}

// TestBroadcastFallbackOnlyForLocalClients pins Python's drop for packets that
// arrive from network interfaces with no transport id, no path, and no link
// (Transport.py:1562-1572 forwards only PLAIN broadcast from local clients;
// everything else in this shape is dropped). Forwarding such packets onto all
// interfaces flooded the public mesh with dead-link keepalives after a hub
// restart: each client's orphaned 0xFF keepalive was rebroadcast network-wide
// instead of being dropped.
func TestBroadcastFallbackOnlyForLocalClients(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	netIface := &capturingInterface{name: "net"}
	otherIface := &capturingInterface{name: "other"}
	ts.RegisterInterface(netIface)
	ts.RegisterInterface(otherIface)

	// A keepalive-shaped HEADER_1 DATA packet for a link this node does not
	// know (a dead link's orphaned 0xFF), arriving from a network interface.
	build := func() []byte {
		raw := []byte{0x0C, 0x00} // HEADER_1, unicast, LINK destination, DATA; hops 0
		raw = append(raw, bytes.Repeat([]byte{0xAB}, TruncatedHashLength/8)...)
		raw = append(raw, byte(ContextKeepalive))
		raw = append(raw, 0xFF)
		return raw
	}

	// From a network interface: Python drops it; nothing may be broadcast.
	ts.Inbound(build(), netIface)
	if got := otherIface.sendCount; got != 0 {
		t.Fatalf("network-sourced orphaned link packet was broadcast on %v interfaces, want 0 (Python drops it)", got)
	}

	// From a spawned local client: the shared instance forwards it on the
	// other interfaces (the established gap-fix for path-less local-client
	// link requests must keep working).
	ts.Inbound(build(), realSpawnedLocalClient(t))
	if got := otherIface.sendCount; got == 0 {
		t.Fatal("local-client-sourced packet was not broadcast to the other interfaces")
	}
}
