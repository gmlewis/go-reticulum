// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import "testing"

// TestOptimiseHardwareMTU pins Python's bitrate-to-MTU table
// (RNS/Interfaces/Interface.py:231-246), including the `>=` first branch, the
// strict `>` branches and the None result at or below 62,500 bps.
func TestOptimiseHardwareMTU(t *testing.T) {
	t.Parallel()
	tests := []struct {
		bitrate int
		want    int
	}{
		{0, 0},
		{62500, 0},
		{62501, 1024},
		{1000000, 1024},
		{1000001, 2048},
		{2000000, 2048},
		{2000001, 4096},
		{5000000, 4096},
		{5000001, 8192},
		{10000000, 8192},
		{10000001, 16384},
		{100000000, 16384},
		{100000001, 32768},
		{200000000, 32768},
		{200000001, 65536},
		{400000000, 65536},
		{400000001, 131072},
		{750000000, 131072},
		{750000001, 262144},
		{999999999, 262144},
		{1000000000, 524288},
		{1500000000, 524288},
	}
	for _, tt := range tests {
		if got := OptimiseHardwareMTU(tt.bitrate); got != tt.want {
			t.Errorf("OptimiseHardwareMTU(%v) = %v, want %v", tt.bitrate, got, tt.want)
		}
	}
}

// TestBaseInterfaceOptimiseMTU pins optimise_mtu's two guards: it only runs
// when AUTOCONFIGURE_MTU is set, so a fixed_mtu survives it, and it leaves an
// interface with neither flag untouched.
func TestBaseInterfaceOptimiseMTU(t *testing.T) {
	t.Parallel()

	unflagged := NewBaseInterface("plain", ModeFull, 1_000_000_000)
	unflagged.SetMTUModel(false, false, 0)
	unflagged.OptimiseMTU()
	if got := unflagged.HardwareMTU(); got != 0 {
		t.Errorf("unflagged HardwareMTU = %v, want 0 (optimise must not run)", got)
	}

	fixed := NewBaseInterface("fixed", ModeFull, 1_000_000_000)
	fixed.SetMTUModel(false, true, 9000)
	fixed.OptimiseMTU()
	if got := fixed.HardwareMTU(); got != 9000 {
		t.Errorf("fixed HardwareMTU = %v, want 9000 (fixed_mtu must survive optimise)", got)
	}

	auto := NewBaseInterface("auto", ModeFull, 0)
	auto.SetMTUModel(true, false, TCPHWMTU)
	auto.SetBitrate(62500)
	auto.OptimiseMTU()
	if got := auto.HardwareMTU(); got != 0 {
		t.Errorf("auto HardwareMTU at 62,500 bps = %v, want 0 (Python None)", got)
	}
	auto.SetBitrate(10_000_001)
	auto.OptimiseMTU()
	if got := auto.HardwareMTU(); got != 16384 {
		t.Errorf("auto HardwareMTU at 10,000,001 bps = %v, want 16384", got)
	}
}

// TestSetFixedMTU pins the TCP fixed_mtu key (TCPInterface.py:78-82): the
// value overrides autoconfiguration, becomes the frame ceiling, and anything
// below Reticulum.MTU is refused.
func TestSetFixedMTU(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		mtu     int
		wantErr bool
	}{
		{499, true},
		{500, false},
		{9000, false},
		{262144, false},
	} {
		tci := &TCPClientInterface{
			BaseInterface: NewBaseInterface("tcp-client", ModeFull, TCPBitrateGuess),
			hwmtu:         TCPHWMTU,
		}
		tci.BaseInterface.SetMTUModel(true, false, TCPHWMTU)
		err := tci.SetFixedMTU(tt.mtu)
		if (err != nil) != tt.wantErr {
			t.Errorf("client SetFixedMTU(%v) error = %v, wantErr %v", tt.mtu, err, tt.wantErr)
			continue
		}
		if tt.wantErr {
			continue
		}
		if tci.AutoConfigureMTU() || !tci.FixedMTU() || tci.HardwareMTU() != tt.mtu {
			t.Errorf("client model after SetFixedMTU(%v) = auto=%v fixed=%v hw=%v, want false/true/%v",
				tt.mtu, tci.AutoConfigureMTU(), tci.FixedMTU(), tci.HardwareMTU(), tt.mtu)
		}
		if tci.hwmtu != tt.mtu {
			t.Errorf("client frame ceiling = %v, want %v", tci.hwmtu, tt.mtu)
		}

		tsi := &TCPServerInterface{
			BaseInterface: NewBaseInterface("tcp-server", ModeFull, TCPBitrateGuess),
			hwmtu:         TCPHWMTU,
		}
		tsi.BaseInterface.SetMTUModel(true, false, TCPHWMTU)
		if err := tsi.SetFixedMTU(tt.mtu); err != nil {
			t.Errorf("server SetFixedMTU(%v) error = %v", tt.mtu, err)
			continue
		}
		if tsi.AutoConfigureMTU() || !tsi.FixedMTU() || tsi.HardwareMTU() != tt.mtu {
			t.Errorf("server model after SetFixedMTU(%v) = auto=%v fixed=%v hw=%v, want false/true/%v",
				tt.mtu, tsi.AutoConfigureMTU(), tsi.FixedMTU(), tsi.HardwareMTU(), tt.mtu)
		}
		if tsi.hwmtu != tt.mtu {
			t.Errorf("server frame ceiling = %v, want %v", tsi.hwmtu, tt.mtu)
		}
	}
}

// TestTCPInterfacesAutoconfigure checks the built-in TCP/Backbone posture:
// both autoconfigure from their guessed bitrate, and the resulting value is
// the table's.
func TestTCPInterfacesAutoconfigure(t *testing.T) {
	t.Parallel()

	tci := &TCPClientInterface{
		BaseInterface: NewBaseInterface("tcp-client", ModeFull, TCPBitrateGuess),
		hwmtu:         TCPHWMTU,
	}
	tci.BaseInterface.SetMTUModel(true, false, TCPHWMTU)
	tci.OptimiseMTU()
	if !tci.AutoConfigureMTU() || tci.FixedMTU() {
		t.Errorf("TCP client flags = auto=%v fixed=%v, want true/false", tci.AutoConfigureMTU(), tci.FixedMTU())
	}
	if got, want := tci.HardwareMTU(), OptimiseHardwareMTU(TCPBitrateGuess); got != want {
		t.Errorf("TCP client HardwareMTU at %v bps = %v, want %v", TCPBitrateGuess, got, want)
	}

	bbi := &BackboneInterface{
		TCPServerInterface: &TCPServerInterface{
			BaseInterface: NewBaseInterface("backbone", ModeFull, 1_000_000_000),
			hwmtu:         BackboneHWMTU,
		},
	}
	bbi.BaseInterface.SetMTUModel(true, false, BackboneHWMTU)
	bbi.OptimiseMTU()
	if got, want := bbi.HardwareMTU(), OptimiseHardwareMTU(1_000_000_000); got != want {
		t.Errorf("Backbone HardwareMTU at 1 Gbps = %v, want %v", got, want)
	}
}
