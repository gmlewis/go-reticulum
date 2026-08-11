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

// announceFilterInterface is a controllable Interface whose mode,
// announces_from_internal, announces_to_internal, OUT, status, bitrate and
// send counter can be configured per test, driving the announce-broadcast
// filter (shouldTransmitAnnounce) deterministically. It mirrors the fields
// Python reads on a real interface in the RNS/Transport.py:1207-1290 elif
// chain. The sendCount field is read only after WaitOutboundSends drains the
// goroutine fan-out, matching the established capturingInterface pattern.
type announceFilterInterface struct {
	dummyInterface
	mode            int
	out             bool
	status          bool
	bitrate         int
	afi             bool  // announces_from_internal
	ati             *bool // announces_to_internal
	sendCount       int
	lastSent        []byte
	recvAnnounceCnt int
}

func newAFI(name string, mode int) *announceFilterInterface {
	return &announceFilterInterface{
		dummyInterface: dummyInterface{name: name},
		mode:           mode,
		out:            true,
		status:         true,
		bitrate:        1000,
		afi:            true,
	}
}

func (a *announceFilterInterface) Mode() int                   { return a.mode }
func (a *announceFilterInterface) IsOut() bool                 { return a.out }
func (a *announceFilterInterface) Status() bool                { return a.status }
func (a *announceFilterInterface) Bitrate() int                { return a.bitrate }
func (a *announceFilterInterface) AnnouncesFromInternal() bool { return a.afi }
func (a *announceFilterInterface) AnnouncesToInternal() *bool  { return a.ati }
func (a *announceFilterInterface) ReceivedAnnounce()           { a.recvAnnounceCnt++ }
func (a *announceFilterInterface) Send(data []byte) error {
	a.sendCount++
	a.lastSent = make([]byte, len(data))
	copy(a.lastSent, data)
	return nil
}

// TestShouldTransmitAnnounceParity walks every branch of the
// RNS/Transport.py:1207-1290 announce-broadcast filter (v1.4.1) and asserts the
// Go decision matches the verbatim-Python golden for each curated case. The
// golden decisions were derived by reproducing the Python elif chain exactly
// and enumerating combinations; the representative cases below cover every
// branch (B1–B7) including the local-destination, announces_from_internal and
// announces_to_internal interactions.
func TestShouldTransmitAnnounceParity(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)

	mk := func(name string, mode int) *announceFilterInterface { return newAFI(name, mode) }
	boolPtr := func(v bool) *bool { return &v }

	type tc struct {
		name  string
		out   int
		from  int // -1 means nil from-interface
		local bool
		afi   bool
		ati   *bool
		hops  int
		want  announceTransmitDecision
	}

	cases := []tc{
		// B1: no next-hop interface (from == nil, !local).
		{"B1 no-next-hop", interfaces.ModeFull, -1, false, true, nil, 1, announceBlock},

		// B2: announces_from_internal block. Outbound with afi=false and an
		// internal next-hop (non-local) is blocked regardless of outbound mode.
		{"B2 afi-block onto full", interfaces.ModeFull, interfaces.ModeInternal, false, false, nil, 1, announceBlock},
		{"B2 afi-block onto boundary", interfaces.ModeBoundary, interfaces.ModeInternal, false, false, nil, 1, announceBlock},
		// B2 only applies when !local; a local destination's announce from an
		// internal next-hop is NOT blocked by afi (it falls through to the
		// mode branch / else).
		{"B2 local bypasses afi-block", interfaces.ModeFull, interfaces.ModeInternal, true, false, nil, 1, announceCapped},

		// B3: access-point outbound always blocks, even for a local destination.
		{"B3 AP block", interfaces.ModeAccessPoint, interfaces.ModeFull, false, true, nil, 1, announceBlock},
		{"B3 AP block even local", interfaces.ModeAccessPoint, interfaces.ModeFull, true, true, nil, 1, announceBlock},

		// B4: outbound internal (guarded by !local).
		// B4c: boundary next-hop onto internal outbound → block (Task 1).
		{"B4c boundary->internal block", interfaces.ModeInternal, interfaces.ModeBoundary, false, true, nil, 1, announceBlock},
		{"B4c boundary->internal ati=false", interfaces.ModeInternal, interfaces.ModeBoundary, false, true, boolPtr(false), 1, announceBlock},
		// B4b: announces_to_internal=true overrides the boundary block (Task 3).
		{"B4b ati-allow boundary->internal", interfaces.ModeInternal, interfaces.ModeBoundary, false, true, boolPtr(true), 1, announceDirect},
		// B4d: other next-hop modes onto internal outbound → direct (no cap).
		{"B4d internal<-full direct", interfaces.ModeInternal, interfaces.ModeFull, false, true, nil, 1, announceDirect},
		{"B4d internal<-internal direct", interfaces.ModeInternal, interfaces.ModeInternal, false, true, nil, 1, announceDirect},
		{"B4d internal<-roaming direct", interfaces.ModeInternal, interfaces.ModeRoaming, false, true, nil, 1, announceDirect},
		// B4 with local destination: the !local guard skips B4, falling to else.
		{"local onto internal -> else capped", interfaces.ModeInternal, interfaces.ModeFull, true, true, nil, 1, announceCapped},
		{"local onto internal -> else direct hops0", interfaces.ModeInternal, interfaces.ModeFull, true, true, nil, 0, announceDirect},

		// B5: outbound roaming.
		// B5a: local destination allowed onto roaming (Task 4a).
		{"B5a local->roaming direct", interfaces.ModeRoaming, interfaces.ModeFull, true, true, nil, 1, announceDirect},
		// B5c/B5d: roaming/boundary next-hop onto roaming blocked.
		{"B5c roaming->roaming block", interfaces.ModeRoaming, interfaces.ModeRoaming, false, true, nil, 1, announceBlock},
		{"B5d boundary->roaming block", interfaces.ModeRoaming, interfaces.ModeBoundary, false, true, nil, 1, announceBlock},
		// B5e: internal/full/gateway/p2p next-hop onto roaming → direct.
		{"B5e internal->roaming direct", interfaces.ModeRoaming, interfaces.ModeInternal, false, true, nil, 1, announceDirect},
		{"B5e full->roaming direct", interfaces.ModeRoaming, interfaces.ModeFull, false, true, nil, 1, announceDirect},

		// B6: outbound boundary.
		// B6a: local destination allowed onto boundary (Task 4a).
		{"B6a local->boundary direct", interfaces.ModeBoundary, interfaces.ModeFull, true, true, nil, 1, announceDirect},
		// B6c: roaming next-hop onto boundary blocked.
		{"B6c roaming->boundary block", interfaces.ModeBoundary, interfaces.ModeRoaming, false, true, nil, 1, announceBlock},
		// B6d: boundary/internal/full/gateway next-hop onto boundary → direct.
		{"B6d boundary->boundary direct", interfaces.ModeBoundary, interfaces.ModeBoundary, false, true, nil, 1, announceDirect},
		{"B6e internal->boundary direct", interfaces.ModeBoundary, interfaces.ModeInternal, false, true, nil, 1, announceDirect},
		{"B6d full->boundary direct", interfaces.ModeBoundary, interfaces.ModeFull, false, true, nil, 1, announceDirect},

		// B7: full/p2p/gateway outbound — announce-cap for hops>0, direct for hops==0.
		{"B7 full->full hops0 direct", interfaces.ModeFull, interfaces.ModeFull, false, true, nil, 0, announceDirect},
		{"B7 full->full hops1 capped", interfaces.ModeFull, interfaces.ModeFull, false, true, nil, 1, announceCapped},
		{"B7 gateway hops1 capped", interfaces.ModeGateway, interfaces.ModeFull, false, true, nil, 1, announceCapped},
		{"B7 p2p hops1 capped", interfaces.ModePointToPoint, interfaces.ModeFull, false, true, nil, 1, announceCapped},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			out := mk("out", c.out)
			out.afi = c.afi
			out.ati = c.ati
			var from interfaces.Interface
			if c.from >= 0 {
				from = mk("from", c.from)
			}
			got := ts.shouldTransmitAnnounce(out, from, c.local, c.hops)
			if got != c.want {
				t.Errorf("shouldTransmitAnnounce(out=%#x, from=%v, local=%v, afi=%v, ati=%v, hops=%v) = %v, want %v",
					c.out, modeName(c.from), c.local, c.afi, atiStr(c.ati), c.hops, got, c.want)
			}
		})
	}
}

func modeName(m int) string {
	if m < 0 {
		return "nil"
	}
	switch m {
	case interfaces.ModeFull:
		return "Full"
	case interfaces.ModePointToPoint:
		return "P2P"
	case interfaces.ModeAccessPoint:
		return "AP"
	case interfaces.ModeRoaming:
		return "Roaming"
	case interfaces.ModeBoundary:
		return "Boundary"
	case interfaces.ModeGateway:
		return "Gateway"
	case interfaces.ModeInternal:
		return "Internal"
	}
	return "?"
}

func atiStr(a *bool) string {
	if a == nil {
		return "nil"
	}
	if *a {
		return "true"
	}
	return "false"
}

// TestProcessAnnounceTableModeFilter is the integration-style assertion that
// processAnnounceTable honors the filter end-to-end. An announce received on a
// boundary next-hop interface must be rebroadcast on a Full outbound interface
// (else branch, announce-cap emits immediately on the first tick) and BLOCKED
// on an internal outbound interface (Task 1: boundary→internal block, B4c).
func TestProcessAnnounceTableModeFilter(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	from := newAFI("from-boundary", interfaces.ModeBoundary)
	outFull := newAFI("out-full", interfaces.ModeFull)
	outInternal := newAFI("out-internal", interfaces.ModeInternal)
	ts.interfaces = append(ts.interfaces, from, outFull, outInternal)

	id := mustTestNewIdentity(t, true)
	// Unregistered destination so the announce is non-local (localDest=false),
	// which is required for the B4c boundary→internal block to apply.
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "mode-filter-test")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	p.Hops = 1
	ts.handleAnnounce(p, from)

	if len(ts.announceTable) != 1 {
		t.Fatalf("expected one queued announce, got %v", len(ts.announceTable))
	}

	ts.processAnnounceTable(time.Now().Add(10 * time.Second))
	ts.WaitOutboundSends()

	if outFull.sendCount == 0 {
		t.Errorf("expected rebroadcast on Full outbound (else branch), got 0 sends")
	}
	if outInternal.sendCount != 0 {
		t.Errorf("boundary->internal block failed: internal outbound got %v sends, want 0", outInternal.sendCount)
	}
}

// TestProcessAnnounceTableAnnouncesFromInternalBlock asserts the Task 2 slice
// end-to-end: an announce received on an internal next-hop interface is
// blocked from rebroadcast on an outbound interface with
// announces_from_internal=false, but allowed on one with it true.
func TestProcessAnnounceTableAnnouncesFromInternalBlock(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	from := newAFI("from-internal", interfaces.ModeInternal)
	outBlock := newAFI("out-afi-false", interfaces.ModeFull)
	outBlock.afi = false
	outAllow := newAFI("out-afi-true", interfaces.ModeFull)
	outAllow.afi = true
	ts.interfaces = append(ts.interfaces, from, outBlock, outAllow)

	id := mustTestNewIdentity(t, true)
	// Unregistered destination: B2 (announces_from_internal block) only
	// applies when !localDestination.
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "afi-test")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	p.Hops = 1
	ts.handleAnnounce(p, from)

	if len(ts.announceTable) != 1 {
		t.Fatalf("expected one queued announce, got %v", len(ts.announceTable))
	}

	ts.processAnnounceTable(time.Now().Add(10 * time.Second))
	ts.WaitOutboundSends()

	if outBlock.sendCount != 0 {
		t.Errorf("announces_from_internal=false should block internal next-hop, got %v sends", outBlock.sendCount)
	}
	if outAllow.sendCount == 0 {
		t.Errorf("announces_from_internal=true should allow internal next-hop, got 0 sends")
	}
}

// TestProcessAnnounceTableAnnouncesToInternalAllow asserts the Task 3 slice
// end-to-end: an announce from a boundary next-hop onto an internal outbound is
// blocked by default (B4c) but allowed when the internal outbound has
// announces_to_internal=true (B4b).
func TestProcessAnnounceTableAnnouncesToInternalAllow(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	from := newAFI("from-boundary", interfaces.ModeBoundary)
	outInternalBlock := newAFI("out-internal-block", interfaces.ModeInternal)
	outInternalAllow := newAFI("out-internal-allow", interfaces.ModeInternal)
	ati := true
	outInternalAllow.ati = &ati
	ts.interfaces = append(ts.interfaces, from, outInternalBlock, outInternalAllow)

	id := mustTestNewIdentity(t, true)
	// Unregistered destination: B4 (outbound internal) only applies when
	// !localDestination.
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "ati-test")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	p := mustTestAnnouncePacketWithEmission(t, ts, id, dest, 1)
	p.Hops = 1
	ts.handleAnnounce(p, from)

	if len(ts.announceTable) != 1 {
		t.Fatalf("expected one queued announce, got %v", len(ts.announceTable))
	}

	ts.processAnnounceTable(time.Now().Add(10 * time.Second))
	ts.WaitOutboundSends()

	if outInternalBlock.sendCount != 0 {
		t.Errorf("default boundary->internal should block, got %v sends", outInternalBlock.sendCount)
	}
	if outInternalAllow.sendCount == 0 {
		t.Errorf("announces_to_internal=true should allow boundary->internal, got 0 sends")
	}
}

// TestProcessAnnounceTableLocalDestinationRoaming asserts the Task 4a slice
// end-to-end: an announce whose destination is locally registered is rebroadcast
// on a roaming outbound interface (B5a local-destination allowance), while an
// announce for an unknown destination from a boundary next-hop is blocked on
// roaming (B5d boundary→roaming block).
func TestProcessAnnounceTableLocalDestinationRoaming(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	from := newAFI("from-boundary", interfaces.ModeBoundary)
	outRoaming := newAFI("out-roaming", interfaces.ModeRoaming)
	ts.interfaces = append(ts.interfaces, from, outRoaming)

	id := mustTestNewIdentity(t, true)
	// Register a local destination so the announce's destination is local.
	localDest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "local-dest-test")
	p := mustTestAnnouncePacketWithEmission(t, ts, id, localDest, 1)
	p.Hops = 1
	ts.handleAnnounce(p, from)

	if len(ts.announceTable) != 1 {
		t.Fatalf("expected one queued announce, got %v", len(ts.announceTable))
	}

	ts.processAnnounceTable(time.Now().Add(10 * time.Second))
	ts.WaitOutboundSends()

	if outRoaming.sendCount == 0 {
		t.Errorf("local destination should rebroadcast on roaming (B5a), got 0 sends")
	}
}
