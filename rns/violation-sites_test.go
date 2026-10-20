// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync/atomic"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// violationInterface is a dummy interface that counts the ingress-violation
// counters Python's protocol_violation / ifac_violation / packet_filter_hit
// increment.
type violationInterface struct {
	dummyInterface

	protocol atomic.Int32
	ifac     atomic.Int32
	filter   atomic.Int32
}

func (v *violationInterface) ProtocolViolation() { v.protocol.Add(1) }
func (v *violationInterface) IFACViolation()     { v.ifac.Add(1) }
func (v *violationInterface) PacketFilterHit()   { v.filter.Add(1) }

// mtuViolationInterface adds a hardware MTU to the counting interface.
type mtuViolationInterface struct {
	violationInterface

	model *interfaces.BaseInterface
}

func (m *mtuViolationInterface) HardwareMTU() int { return m.model.HardwareMTU() }

// TestPacketFilterViolationSites pins the packet_filter branches that Python
// charges to the receiving interface (Transport.py:1593-1622): transported
// PLAIN/GROUP packets, PLAIN/GROUP announces, and a repeated announce with an
// invalid destination type. Python also charges packet_filter_hit at the call
// site, which the Inbound caller does separately.
func TestPacketFilterViolationSites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		packet     *Packet
		remembered bool
		wantFilter bool
	}{
		{
			name:   "transported PLAIN data",
			packet: &Packet{PacketType: PacketData, DestinationType: DestinationPlain, Hops: 2},
		},
		{name: "PLAIN announce", packet: &Packet{PacketType: PacketAnnounce, DestinationType: DestinationPlain, Hops: 0}},
		{
			name:   "transported GROUP data",
			packet: &Packet{PacketType: PacketData, DestinationType: DestinationGroup, Hops: 2},
		},
		{name: "GROUP announce", packet: &Packet{PacketType: PacketAnnounce, DestinationType: DestinationGroup, Hops: 0}},
		{
			name:       "repeated announce with invalid destination type",
			packet:     &Packet{PacketType: PacketAnnounce, DestinationType: DestinationLink, Hops: 1, PacketHash: []byte{0xAA}},
			remembered: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := NewTransportSystem(nil)
			iface := &violationInterface{dummyInterface: dummyInterface{name: "violation"}}
			tt.packet.ReceivingInterface = iface
			if tt.remembered {
				ts.mu.Lock()
				ts.packetHashes[string(tt.packet.PacketHash)] = ts.startedAt
				ts.mu.Unlock()
			}

			accepted := ts.packetFilter(tt.packet)
			if accepted {
				t.Fatalf("packetFilter accepted a packet Python drops")
			}
			if got := iface.protocol.Load(); got != 1 {
				t.Errorf("protocol violations = %v, want 1", got)
			}
			if got := iface.filter.Load(); got != 0 {
				t.Errorf("packet_filter_hits = %v, want 0 (charged by the Inbound caller, not the filter)", got)
			}
		})
	}
}

// TestInboundFrameExceedsMTUViolation pins Python's frame-length gate
// (Transport.py:1699-1701): a frame above the interface's hardware MTU is a
// protocol violation, and an ordinary frame is not counted.
func TestInboundFrameExceedsMTUViolation(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	iface := &mtuViolationInterface{
		violationInterface: violationInterface{dummyInterface: dummyInterface{name: "mtu"}},
		model:              interfaces.NewBaseInterface("mtu", interfaces.ModeFull, 62500),
	}
	iface.model.SetMTUModel(false, true, 1024)

	ts.Inbound(make([]byte, 1025), iface)
	if got := iface.protocol.Load(); got != 1 {
		t.Fatalf("protocol violations after an oversized frame = %v, want 1", got)
	}
	if got := iface.ifac.Load(); got != 0 {
		t.Errorf("ifac violations = %v, want 0", got)
	}

	// A valid frame that fits must not be charged.
	_, raw, _ := readyWaitDest(t, ts, "mtu-fit", "hello")
	iface.protocol.Store(0)
	ts.Inbound(raw, iface)
	if got := iface.protocol.Load(); got != 0 {
		t.Errorf("protocol violations after an in-MTU frame = %v, want 0", got)
	}
}

// TestTaglessPathRequestViolation pins Python's tagless-PR and excessive-tag
// charges (Transport.py:1748-1753).
func TestTaglessPathRequestViolation(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	iface := &violationInterface{dummyInterface: dummyInterface{name: "pr"}}
	packet := &Packet{ReceivingInterface: iface}
	hashLen := TruncatedHashLength / 8

	target := make([]byte, hashLen)
	for i := range target {
		target[i] = byte(i + 1)
	}
	ts.handlePathRequest(target, packet)
	if got := iface.protocol.Load(); got != 1 {
		t.Errorf("tagless path request protocol violations = %v, want 1", got)
	}

	// A tag longer than the truncated hash is truncated and charged. The
	// requestor transport id occupies the middle segment, so the tag starts
	// after two hashes (Python Transport.py:1740-1753).
	ts2 := NewTransportSystem(nil)
	iface2 := &violationInterface{dummyInterface: dummyInterface{name: "pr2"}}
	packet2 := &Packet{ReceivingInterface: iface2}
	data := append(append([]byte(nil), target...), make([]byte, hashLen)...)
	data = append(data, make([]byte, hashLen+4)...)
	ts2.handlePathRequest(data, packet2)
	if got := iface2.protocol.Load(); got != 1 {
		t.Errorf("excessive-tag path request protocol violations = %v, want 1", got)
	}
}

// TestLinkPacketBeforeValidationViolation pins Python's pre-validation link
// gate (Transport.py:2036-2037): a link packet for an entry whose proof has
// not been seen is counted and dropped, not forwarded.
func TestLinkPacketBeforeValidationViolation(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	ts.SetEnabled(true)
	iface := &violationInterface{dummyInterface: dummyInterface{name: "link-rx"}}
	out := &violationInterface{dummyInterface: dummyInterface{name: "link-out"}}

	linkID := make([]byte, TruncatedHashLength/8)
	for i := range linkID {
		linkID[i] = byte(0x40 + i)
	}
	ts.mu.Lock()
	ts.ensureStateLocked()
	ts.linkTable[string(linkID)] = &LinkEntry{
		ReceivedInterface: iface,
		OutboundInterface: out,
		RemainingHops:     1,
		Validated:         false,
	}
	ts.mu.Unlock()

	packet := &Packet{
		PacketType:         PacketData,
		DestinationHash:    linkID,
		ReceivingInterface: iface,
		Hops:               1,
	}
	ts.processInboundPacket(packet)

	if got := iface.protocol.Load(); got != 1 {
		t.Errorf("pre-validation link packet protocol violations = %v, want 1", got)
	}
}

// TestInvalidAnnounceViolation pins Python's invalid-announce charge
// (Transport.py:2089-2090).
func TestInvalidAnnounceViolation(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	iface := &violationInterface{dummyInterface: dummyInterface{name: "announce"}}
	packet := &Packet{
		PacketType:         PacketAnnounce,
		DestinationType:    DestinationSingle,
		ReceivingInterface: iface,
	}
	ts.handleAnnounce(packet, iface)
	if got := iface.protocol.Load(); got != 1 {
		t.Errorf("invalid announce protocol violations = %v, want 1", got)
	}
}
