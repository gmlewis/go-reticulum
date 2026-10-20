// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import "testing"

// TestBaseInterfaceTrafficCounters pins Interface.received_announce/
// sent_announce/received_path_request/sent_path_request's counter half
// (Interface.py:282-304): each call increments its count and adds the given
// size to the byte total.
func TestBaseInterfaceTrafficCounters(t *testing.T) {
	t.Parallel()

	bi := NewBaseInterface("test", 1, 1000)
	bi.ReceivedAnnounceSize(100)
	bi.ReceivedAnnounceSize(50)
	bi.SentAnnounceSize(70)
	bi.ReceivedPathRequestSize(10)
	bi.SentPathRequestSize(20)

	rxb, txb, rxc, txc := bi.AnnounceCounters()
	if rxb != 150 || txb != 70 || rxc != 2 || txc != 1 {
		t.Errorf("announce counters = (%v,%v,%v,%v), want (150,70,2,1)", rxb, txb, rxc, txc)
	}
	prxb, ptxb, prxc, ptxc := bi.PathRequestCounters()
	if prxb != 10 || ptxb != 20 || prxc != 1 || ptxc != 1 {
		t.Errorf("PR counters = (%v,%v,%v,%v), want (10,20,1,1)", prxb, ptxb, prxc, ptxc)
	}

	// The size-aware forms propagate both count and bytes to a parent
	// (Interface.py:282-304 passes from_spawned=True).
	parent := NewBaseInterface("parent", 1, 1000)
	child := NewBaseInterface("child", 1, 1000)
	child.parentInterface = parent
	child.ReceivedAnnounceSize(33)
	child.SentPathRequestSize(44)
	rxb, _, rxc, _ = parent.AnnounceCounters()
	if rxb != 33 || rxc != 1 {
		t.Errorf("parent announce counters = (%v,%v), want (33,1)", rxb, rxc)
	}
	_, ptxb, _, ptxc = parent.PathRequestCounters()
	if ptxb != 44 || ptxc != 1 {
		t.Errorf("parent PR counters = (%v,%v), want (44,1)", ptxb, ptxc)
	}
}

// TestBaseInterfaceViolationCounters pins Interface.protocol_violation,
// ifac_violation and packet_filter_hit (Interface.py:307-317).
func TestBaseInterfaceViolationCounters(t *testing.T) {
	t.Parallel()

	bi := NewBaseInterface("test", 1, 1000)
	bi.ProtocolViolation()
	bi.ProtocolViolation()
	bi.IFACViolation()
	bi.PacketFilterHit()

	protocol, ifac, filter := bi.ViolationCounters()
	if protocol != 2 || ifac != 1 || filter != 1 {
		t.Errorf("violations = (%v,%v,%v), want (2,1,1)", protocol, ifac, filter)
	}
}
