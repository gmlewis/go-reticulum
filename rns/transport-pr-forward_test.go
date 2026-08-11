// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// prForwardInterface is a controllable Interface for exercising
// forwardPathRequest's recursive path-request emission. Its mode, online
// status, OUT flag, recursive_prs policy and send counter are settable per
// test, mirroring the fields Python's path_request() reads on the attached
// interface (RNS/Transport.py:3006-3011) and on each candidate egress
// interface (Transport.py:3124-3135). The sendCount is read only after
// WaitOutboundSends drains the goroutine fan-out.
type prForwardInterface struct {
	dummyInterface
	mode         int
	out          bool
	status       bool
	recursivePrs bool
	sendCount    int
}

func newPRI(name string, mode int) *prForwardInterface {
	return &prForwardInterface{
		dummyInterface: dummyInterface{name: name},
		mode:           mode,
		out:            true,
		status:         true,
	}
}

func (p *prForwardInterface) Mode() int          { return p.mode }
func (p *prForwardInterface) IsOut() bool        { return p.out }
func (p *prForwardInterface) Status() bool       { return p.status }
func (p *prForwardInterface) RecursivePrs() bool { return p.recursivePrs }
func (p *prForwardInterface) Send(data []byte) error {
	p.sendCount++
	return nil
}

// prForwardPacket builds a minimal path-request packet whose Data carries a
// TRUNCATED_HASHLENGTH target hash, which is all forwardPathRequest reads off
// the incoming packet before constructing its own relay request.
func prForwardPacket(t *testing.T) *Packet {
	t.Helper()
	target := bytes.Repeat([]byte{0x42}, TruncatedHashLength/8)
	return &Packet{Data: target, PacketType: PacketData}
}

// TestForwardPathRequestBoundarySearchModeFilter asserts Phase 6 Task 5: a
// path request received on a boundary-mode attached interface (recursive_prs
// disabled) must only egress recursively on boundary/gateway interfaces that
// are online. This mirrors RNS/Transport.py:3009-3011, which sets
// search_mode_filter = BOUNDARY_SEARCH_MODES = [MODE_BOUNDARY, MODE_GATEWAY]
// for a boundary attached interface, and Transport.py:3124-3127, which skips
// any candidate interface whose mode is not in the filter.
func TestForwardPathRequestBoundarySearchModeFilter(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	source := newPRI("src-boundary", interfaces.ModeBoundary)
	outBoundary := newPRI("out-boundary", interfaces.ModeBoundary)
	outGateway := newPRI("out-gateway", interfaces.ModeGateway)
	outFull := newPRI("out-full", interfaces.ModeFull)
	outAP := newPRI("out-ap", interfaces.ModeAccessPoint)
	outBoundaryDown := newPRI("out-boundary-down", interfaces.ModeBoundary)
	outBoundaryDown.status = false
	ts.interfaces = append(ts.interfaces, source, outBoundary, outGateway, outFull, outAP, outBoundaryDown)

	ts.forwardPathRequest(prForwardPacket(t), source)
	ts.WaitOutboundSends()

	if outBoundary.sendCount != 1 {
		t.Errorf("boundary egress: got %d sends, want 1", outBoundary.sendCount)
	}
	if outGateway.sendCount != 1 {
		t.Errorf("gateway egress: got %d sends, want 1", outGateway.sendCount)
	}
	if outFull.sendCount != 0 {
		t.Errorf("full egress should be filtered out by boundary search_mode_filter: got %d sends, want 0", outFull.sendCount)
	}
	if outAP.sendCount != 0 {
		t.Errorf("access_point egress should be filtered out by boundary search_mode_filter: got %d sends, want 0", outAP.sendCount)
	}
	if outBoundaryDown.sendCount != 0 {
		t.Errorf("offline boundary egress should be skipped (online check): got %d sends, want 0", outBoundaryDown.sendCount)
	}
}

// TestForwardPathRequestRecursivePrsOverridesMode asserts Phase 6 Task 6:
// when the attached interface has recursive_prs enabled, recursive path
// requests egress on all online interfaces regardless of the attached
// interface's mode. Per RNS/Transport.py:3007-3008 the recursive_prs branch
// is checked first in the should_search_for_unknown elif chain, so it takes
// precedence over the boundary branch and leaves search_mode_filter unset.
// The contrast with TestForwardPathRequestBoundarySearchModeFilter (same
// boundary mode, recursive_prs disabled) demonstrates the override: with
// recursive_prs=true, Full and access_point egress interfaces also receive
// the relayed request.
func TestForwardPathRequestRecursivePrsOverridesMode(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	source := newPRI("src-boundary-rprs", interfaces.ModeBoundary)
	source.recursivePrs = true
	outFull := newPRI("out-full", interfaces.ModeFull)
	outAP := newPRI("out-ap", interfaces.ModeAccessPoint)
	outBoundary := newPRI("out-boundary", interfaces.ModeBoundary)
	outGateway := newPRI("out-gateway", interfaces.ModeGateway)
	ts.interfaces = append(ts.interfaces, source, outFull, outAP, outBoundary, outGateway)

	ts.forwardPathRequest(prForwardPacket(t), source)
	ts.WaitOutboundSends()

	for _, oi := range []*prForwardInterface{outFull, outAP, outBoundary, outGateway} {
		if oi.sendCount != 1 {
			t.Errorf("%s egress with recursive_prs=true: got %d sends, want 1 (mode should not filter)", oi.Name(), oi.sendCount)
		}
	}
}

// TestForwardPathRequestRecursivePrsFullModeSource covers the "regardless of
// mode" wording of Task 6 directly: a Full-mode attached interface (Full is
// not in DISCOVER_PATHS_FOR and not boundary) with recursive_prs=true still
// emits recursive path requests on all online interfaces.
func TestForwardPathRequestRecursivePrsFullModeSource(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)

	source := newPRI("src-full-rprs", interfaces.ModeFull)
	source.recursivePrs = true
	outBoundary := newPRI("out-boundary", interfaces.ModeBoundary)
	outAP := newPRI("out-ap", interfaces.ModeAccessPoint)
	ts.interfaces = append(ts.interfaces, source, outBoundary, outAP)

	ts.forwardPathRequest(prForwardPacket(t), source)
	ts.WaitOutboundSends()

	if outBoundary.sendCount != 1 {
		t.Errorf("boundary egress from full+rprs source: got %d sends, want 1", outBoundary.sendCount)
	}
	if outAP.sendCount != 1 {
		t.Errorf("access_point egress from full+rprs source: got %d sends, want 1", outAP.sendCount)
	}
}
