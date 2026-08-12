// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// TestLinkWatchdogOutboundTriggerSendsKeepalive covers Phase 10 task 3: the
// v1.4.0 link-watchdog fix that adds the outbound-inactivity trigger. Python
// Link.py:749 fires the keepalive/stale check when
// `now >= last_inbound + keepalive OR now >= last_outbound + keepalive`.
//
// The scenario is a silent initiator that is still receiving destination
// traffic: last_inbound is fresh (so the inbound-only trigger would NOT fire
// and the link cannot go stale), but last_outbound is a full keepalive interval
// in the past. With the fix the outbound trigger fires, the initiator sends a
// keepalive, and the link stays Active. Without the fix the trigger never
// fires and no keepalive is sent, so the remote side would eventually time the
// silent initiator out.
func TestLinkWatchdogOutboundTriggerSendsKeepalive(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")
	link := mustTestNewLink(t, ts, receiverDest)
	iface := &capturingInterface{name: "capture"}

	link.initiator = true
	link.status.Store(LinkActive)
	link.linkID = []byte("outbound_trigger_link")
	link.hash = link.linkID
	link.attachedInterface = iface
	link.keepalive = 5 * time.Second
	link.staleTime = 10 * time.Second
	now := time.Unix(1700000000, 0)
	link.activatedAt = now.Add(-1 * time.Second)
	link.lastInbound = now                         // fresh: receiving destination traffic
	link.lastOutbound = now.Add(-20 * time.Second) // old: silent initiator
	link.lastKeepalive = now.Add(-20 * time.Second)

	sleep := link.watchdogStep(now)

	// The outbound-inactivity trigger fires even though last_inbound is
	// fresh, so a keepalive is sent.
	if iface.sendCount != 1 {
		t.Fatalf("outbound trigger: keepalive sendCount=%v, want 1 (no keepalive sent on outbound inactivity)", iface.sendCount)
	}
	// Fresh inbound traffic prevents staleness — the link stays Active.
	if got := link.status.Load(); got != LinkActive {
		t.Fatalf("link status=%v, want LinkActive (fresh inbound prevents stale)", got)
	}
	// The non-stale trigger branch sleeps for one keepalive interval.
	if sleep != link.keepalive {
		t.Fatalf("watchdog sleep=%v, want keepalive=%v", sleep, link.keepalive)
	}
}

// TestLinkWatchdogOutboundTriggerDoesNotStaleOnFreshInbound asserts the
// outbound trigger does not promote a fresh-inbound link to stale: staleness
// remains gated on last_inbound + stale_time (Python Link.py:753), so a link
// that is receiving traffic but has not sent for longer than stale_time still
// stays Active.
func TestLinkWatchdogOutboundTriggerDoesNotStaleOnFreshInbound(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")
	link := mustTestNewLink(t, ts, receiverDest)
	iface := &capturingInterface{name: "capture"}

	link.initiator = false // responder does not send keepalives
	link.status.Store(LinkActive)
	link.linkID = []byte("responder_inbound_fresh")
	link.hash = link.linkID
	link.attachedInterface = iface
	link.keepalive = 5 * time.Second
	link.staleTime = 10 * time.Second
	now := time.Unix(1700000000, 0)
	link.activatedAt = now.Add(-1 * time.Second)
	link.lastInbound = now                          // fresh inbound
	link.lastOutbound = now.Add(-100 * time.Second) // far past stale_time
	link.lastKeepalive = now.Add(-100 * time.Second)

	sleep := link.watchdogStep(now)

	// The outbound trigger fires (last_outbound is ancient), but the responder
	// sends no keepalive (only initiators do), and the fresh inbound keeps the
	// link from going stale.
	if iface.sendCount != 0 {
		t.Fatalf("responder sent keepalive sendCount=%v, want 0 (only initiators send keepalives)", iface.sendCount)
	}
	if got := link.status.Load(); got != LinkActive {
		t.Fatalf("link status=%v, want LinkActive (fresh inbound prevents stale even with ancient outbound)", got)
	}
	if sleep != link.keepalive {
		t.Fatalf("watchdog sleep=%v, want keepalive=%v", sleep, link.keepalive)
	}
}

// TestLinkKeepaliveResponseGuard covers Phase 10 task 3: the v1.4.0 keepalive
// response guard. Python Link.py:1124-1127 only echoes a 0xFE keepalive when
// `time.time() >= self.last_outbound + self.keepalive` — recent outbound
// traffic already serves as the keepalive echo, so a redundant 0xFE would
// just waste bandwidth. A responder that sent traffic less than a keepalive
// interval ago must NOT respond; one that has been silent for at least a
// keepalive interval must respond.
func TestLinkKeepaliveResponseGuard(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	receiverID := mustTestNewIdentity(t, true)
	receiverDest := mustTestNewDestination(t, ts, receiverID, DestinationIn, DestinationSingle, "receiver")
	link := mustTestNewLink(t, ts, receiverDest)
	iface := &capturingInterface{name: "capture"}

	link.initiator = false // responder
	link.status.Store(LinkActive)
	link.linkID = []byte("keepalive_response_guard")
	link.hash = link.linkID
	link.attachedInterface = iface
	link.keepalive = 5 * time.Second
	now := time.Unix(1700000000, 0)
	link.now = func() time.Time { return now }

	// Case 1: fresh last_outbound (1s ago, well within the 5s keepalive).
	// A 0xFF keepalive must NOT elicit a 0xFE response.
	link.lastOutbound = now.Add(-1 * time.Second)
	link.receive(&Packet{Context: ContextKeepalive, Data: []byte{0xFF}})
	if iface.sendCount != 0 {
		t.Fatalf("fresh lastOutbound: 0xFE response sent (sendCount=%v), want 0", iface.sendCount)
	}

	// Case 2: old last_outbound (20s ago, past the 5s keepalive interval).
	// A 0xFF keepalive MUST elicit a 0xFE response.
	link.lastOutbound = now.Add(-20 * time.Second)
	link.receive(&Packet{Context: ContextKeepalive, Data: []byte{0xFF}})
	if iface.sendCount != 1 {
		t.Fatalf("old lastOutbound: 0xFE response sendCount=%v, want 1", iface.sendCount)
	}
}
