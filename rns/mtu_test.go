// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/json"
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
	"github.com/gmlewis/go-reticulum/testutils"
)

// mtuTestInterface is a full Interface (via dummyInterface) that carries
// Python's MTU model.
type mtuTestInterface struct {
	dummyInterface

	model *interfaces.BaseInterface
}

func (m *mtuTestInterface) HardwareMTU() int       { return m.model.HardwareMTU() }
func (m *mtuTestInterface) AutoConfigureMTU() bool { return m.model.AutoConfigureMTU() }
func (m *mtuTestInterface) FixedMTU() bool         { return m.model.FixedMTU() }

// mtuInterface builds a fake interface carrying the given MTU model, which is
// all next_hop_interface_hw_mtu inspects.
func mtuInterface(name string, autoConfigure, fixed bool, hwMTU int) *mtuTestInterface {
	bi := interfaces.NewBaseInterface(name, interfaces.ModeFull, 62500)
	bi.SetMTUModel(autoConfigure, fixed, hwMTU)
	return &mtuTestInterface{dummyInterface: dummyInterface{name: name}, model: bi}
}

// TestNextHopInterfaceHWMTU pins Python's accessor (Transport.py:3101-3107):
// the next hop's HW_MTU is returned only when it AUTOCONFIGURE_MTU or
// FIXED_MTU; every other case is nil, including an unknown destination and an
// interface with HW_MTU None.
func TestNextHopInterfaceHWMTU(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		iface        interfaces.Interface
		auto, fixed  bool
		hw           int
		known        bool
		want         int
		wantReturned bool
	}{
		{name: "unflagged interface is nil", known: true, auto: false, fixed: false, hw: 4096},
		{name: "autoconfigured interface returns its MTU", known: true, auto: true, hw: 8192, want: 8192, wantReturned: true},
		{name: "fixed interface returns its MTU", known: true, fixed: true, hw: 9000, want: 9000, wantReturned: true},
		{name: "autoconfigured None is nil", known: true, auto: true, hw: 0},
		{name: "unknown destination is nil", known: false, auto: true, hw: 8192},
		{name: "nil path interface is nil", known: true, auto: true, hw: 8192, iface: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := NewTransportSystem(nil)
			dest := make([]byte, TruncatedHashLength/8)
			for i := range dest {
				dest[i] = byte(i + 1)
			}
			if tt.known {
				iface := tt.iface
				if iface == nil && tt.name != "nil path interface is nil" {
					iface = mtuInterface("next-hop", tt.auto, tt.fixed, tt.hw)
				}
				ts.mu.Lock()
				ts.pathTable[string(dest)] = &PathEntry{Interface: iface}
				ts.mu.Unlock()
			}
			got, ok := ts.NextHopInterfaceHWMTU(dest)
			if ok != tt.wantReturned || (ok && got != tt.want) {
				t.Errorf("NextHopInterfaceHWMTU = (%v, %v), want (%v, %v)", got, ok, tt.want, tt.wantReturned)
			}
		})
	}
}

// TestLinkSignallingUsesNextHopHWMTU pins Python's link MTU signalling
// (Link.py:305-310): with discovery enabled, a next-hop interface that
// autoconfigures or fixes its MTU is signalled; otherwise Reticulum.MTU is.
func TestLinkSignallingUsesNextHopHWMTU(t *testing.T) {
	t.Parallel()

	decodedMTU := func(b []byte) int {
		if len(b) != LinkMTUSize {
			t.Fatalf("signalling bytes length = %v, want %v", len(b), LinkMTUSize)
		}
		v := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
		return int(v & MTUBytemask)
	}

	tests := []struct {
		name      string
		discovery bool
		iface     interfaces.Interface
		wantNil   bool
		wantMTU   int
	}{
		{name: "discovery disabled signals nothing", discovery: false, iface: mtuInterface("nh", true, false, 8192), wantNil: true},
		{name: "no next hop signals the default MTU", discovery: true, wantMTU: MTU},
		{name: "autoconfigured next hop is signalled", discovery: true, iface: mtuInterface("nh", true, false, 8192), wantMTU: 8192},
		{name: "fixed next hop is signalled", discovery: true, iface: mtuInterface("nh", false, true, 9000), wantMTU: 9000},
		{name: "unflagged next hop keeps the default MTU", discovery: true, iface: mtuInterface("nh", false, false, 4096), wantMTU: MTU},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := NewTransportSystem(nil)
			ts.SetLinkMTUDiscovery(tt.discovery)
			dest := mustTestNewDestination(t, ts, mustTestNewIdentity(t, true), DestinationOut, DestinationSingle, "mtu", "signalling")
			if tt.iface != nil {
				ts.mu.Lock()
				ts.pathTable[string(dest.Hash)] = &PathEntry{Interface: tt.iface}
				ts.mu.Unlock()
			}
			link := &Link{
				transport:   ts,
				destination: dest,
				mtu:         MTU,
				mode:        LinkModeAES256CBC,
			}
			got := link.signallingBytes()
			if tt.wantNil {
				if got != nil {
					t.Errorf("signallingBytes = %x, want nil", got)
				}
				return
			}
			if mtu := decodedMTU(got); mtu != tt.wantMTU {
				t.Errorf("signalled MTU = %v, want %v", mtu, tt.wantMTU)
			}
		})
	}
}

// TestNextHopInterfaceHWMTUPythonParity drives the installed reference's
// Transport.next_hop_interface_hw_mtu over the same model battery and compares
// the answers.
func TestNextHopInterfaceHWMTUPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	type modelCase struct {
		Auto  bool `json:"auto"`
		Fixed bool `json:"fixed"`
		HW    int  `json:"hw"`
		Known bool `json:"known"`
	}
	cases := []modelCase{
		{Auto: false, Fixed: false, HW: 4096, Known: true},
		{Auto: true, Fixed: false, HW: 8192, Known: true},
		{Auto: false, Fixed: true, HW: 9000, Known: true},
		{Auto: true, Fixed: true, HW: 16384, Known: true},
		{Auto: true, Fixed: false, HW: 0, Known: true},
		{Auto: true, Fixed: false, HW: 8192, Known: false},
	}

	const script = `
import json, sys
import RNS
from RNS.Transport import IDX_PT_RVCD_IF

class Fake: pass

results = []
for i, case in enumerate(json.loads(sys.argv[1])):
    dest = bytes([i + 1]) * (RNS.Identity.TRUNCATED_HASHLENGTH // 8)
    if case["known"]:
        iface = Fake()
        iface.AUTOCONFIGURE_MTU = case["auto"]
        iface.FIXED_MTU = case["fixed"]
        iface.HW_MTU = case["hw"] if case["hw"] else None
        entry = [None] * (IDX_PT_RVCD_IF + 1)
        entry[IDX_PT_RVCD_IF] = iface
        RNS.Transport.path_table[dest] = entry
    else:
        RNS.Transport.path_table.pop(dest, None)
    results.append(RNS.Transport.next_hop_interface_hw_mtu(dest))
json.dump(results, sys.stdout)
`
	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	var want []any
	if err := json.Unmarshal([]byte(testutils.RunPython(t, script, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	if len(want) != len(cases) {
		t.Fatalf("python returned %v results, want %v", len(want), len(cases))
	}

	for i, tt := range cases {
		ts := NewTransportSystem(nil)
		dest := make([]byte, TruncatedHashLength/8)
		for j := range dest {
			dest[j] = byte(i + 1)
		}
		if tt.Known {
			ts.mu.Lock()
			ts.pathTable[string(dest)] = &PathEntry{Interface: mtuInterface("next-hop", tt.Auto, tt.Fixed, tt.HW)}
			ts.mu.Unlock()
		}
		got, ok := ts.NextHopInterfaceHWMTU(dest)
		if want[i] == nil {
			if ok {
				t.Errorf("case %v: NextHopInterfaceHWMTU = (%v, true), want nil (Python)", i, got)
			}
			continue
		}
		wantMTU := int(want[i].(float64))
		if !ok || got != wantMTU {
			t.Errorf("case %v: NextHopInterfaceHWMTU = (%v, %v), want (%v, true) (Python)", i, got, ok, wantMTU)
		}
	}
}

// TestOptimiseMTUPythonParity derives the bitrate-to-MTU table from the
// installed reference's Interface.optimise_mtu (Interface.py:231-246) over the
// branch boundaries and compares OptimiseHardwareMTU.
func TestOptimiseMTUPythonParity(t *testing.T) {
	t.Parallel()
	testutils.SkipIfNoPythonRNS(t)

	bitrates := []int{
		0, 62500, 62501, 1000000, 1000001, 2000000, 2000001,
		5000000, 5000001, 10000000, 10000001, 100000000, 100000001,
		200000000, 200000001, 400000000, 400000001, 750000000,
		750000001, 999999999, 1000000000, 1500000000,
	}
	in, err := json.Marshal(bitrates)
	if err != nil {
		t.Fatal(err)
	}

	const script = `
import json, sys
from RNS.Interfaces.Interface import Interface

class Auto(Interface):
    AUTOCONFIGURE_MTU = True

iface = object.__new__(Auto)
results = []
for bitrate in json.loads(sys.argv[1]):
    iface.bitrate = bitrate
    iface.optimise_mtu()
    results.append(iface.HW_MTU)
json.dump(results, sys.stdout)
`
	var want []any
	if err := json.Unmarshal([]byte(testutils.RunPython(t, script, string(in))), &want); err != nil {
		t.Fatalf("decode python output: %v", err)
	}
	for i, bitrate := range bitrates {
		got := interfaces.OptimiseHardwareMTU(bitrate)
		if want[i] == nil {
			if got != 0 {
				t.Errorf("OptimiseHardwareMTU(%v) = %v, want 0 (Python None)", bitrate, got)
			}
			continue
		}
		if wantMTU := int(want[i].(float64)); got != wantMTU {
			t.Errorf("OptimiseHardwareMTU(%v) = %v, want %v (Python)", bitrate, got, wantMTU)
		}
	}
}
