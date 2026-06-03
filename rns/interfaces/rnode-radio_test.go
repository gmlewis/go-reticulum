// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"testing"
)

func TestRNodeSetFrequency(t *testing.T) {
	t.Parallel()
	frame := RNodeSetFrequency(433050000)
	if frame[0] != KISSFend || frame[len(frame)-1] != KISSFend {
		t.Fatal("frame must start and end with FEND")
	}
	if frame[1] != KISSCmdFrequency {
		t.Fatalf("command byte must be CMD_FREQUENCY (0x01), got 0x%02X", frame[1])
	}
	data := KISSUnescape(frame[2 : len(frame)-1])
	var got uint32
	got = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	if got != 433050000 {
		t.Fatalf("expected frequency 433050000, got %d", got)
	}
}

func TestRNodeSetFrequencySelectInt(t *testing.T) {
	t.Parallel()
	frame := RNodeSetFrequencySelectInt(868000000, 2)
	if frame[0] != KISSFend || frame[3] != KISSFend {
		t.Fatal("select prefix must be [FEND][CMD_SEL_INT][index][FEND]")
	}
	if frame[1] != KISSCmdSelInt || frame[2] != 2 {
		t.Fatal("select prefix must select sub-interface index 2")
	}
	if frame[4] != KISSFend {
		t.Fatal("second command must start with FEND")
	}
	if frame[5] != KISSCmdFrequency {
		t.Fatalf("command byte must be CMD_FREQUENCY, got 0x%02X", frame[5])
	}
}

func TestRNodeSetBandwidth(t *testing.T) {
	t.Parallel()
	frame := RNodeSetBandwidth(125000)
	data := KISSUnescape(frame[2 : len(frame)-1])
	var got uint32
	got = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	if got != 125000 {
		t.Fatalf("expected bandwidth 125000, got %d", got)
	}
}

func TestRNodeSetTXPower(t *testing.T) {
	t.Parallel()
	frame := RNodeSetTXPower(17)
	if frame[1] != KISSCmdTXPower {
		t.Fatalf("command byte must be CMD_TXPOWER (0x03), got 0x%02X", frame[1])
	}
	data := KISSUnescape(frame[2 : len(frame)-1])
	if len(data) != 1 || data[0] != 17 {
		t.Fatalf("expected txpower 17, got %v", data)
	}
}

func TestRNodeSetTXPowerSigned(t *testing.T) {
	t.Parallel()
	frame := RNodeSetTXPowerSigned(-5)
	data := KISSUnescape(frame[2 : len(frame)-1])
	if len(data) != 1 || int8(data[0]) != -5 {
		t.Fatalf("expected signed txpower -5, got %d", int8(data[0]))
	}
}

func TestRNodeSetTXPowerSelectInt(t *testing.T) {
	t.Parallel()
	frame := RNodeSetTXPowerSelectInt(-9, 1)
	if frame[1] != KISSCmdSelInt || frame[2] != 1 {
		t.Fatal("select prefix must select sub-interface index 1")
	}
	cmdStart := 5
	if frame[cmdStart] != KISSCmdTXPower {
		t.Fatalf("command byte must be CMD_TXPOWER, got 0x%02X", frame[cmdStart])
	}
}

func TestRNodeSetSpreadingFactor(t *testing.T) {
	t.Parallel()
	frame := RNodeSetSpreadingFactor(7)
	if frame[1] != KISSCmdSF {
		t.Fatalf("command byte must be CMD_SF (0x04), got 0x%02X", frame[1])
	}
	data := KISSUnescape(frame[2 : len(frame)-1])
	if len(data) != 1 || data[0] != 7 {
		t.Fatalf("expected SF 7, got %v", data)
	}
}

func TestRNodeSetCodingRate(t *testing.T) {
	t.Parallel()
	frame := RNodeSetCodingRate(5)
	if frame[1] != KISSCmdCR {
		t.Fatalf("command byte must be CMD_CR (0x05), got 0x%02X", frame[1])
	}
	data := KISSUnescape(frame[2 : len(frame)-1])
	if len(data) != 1 || data[0] != 5 {
		t.Fatalf("expected CR 5, got %v", data)
	}
}

func TestRNodeSetSTALock(t *testing.T) {
	t.Parallel()
	t.Run("non-nil value", func(t *testing.T) {
		t.Parallel()
		pct := 15.5
		frame := RNodeSetSTALock(&pct)
		if frame == nil {
			t.Fatal("expected non-nil frame for non-nil percentage")
		}
		if frame[1] != KISSCmdSTALock {
			t.Fatalf("command byte must be CMD_ST_ALOCK (0x0B), got 0x%02X", frame[1])
		}
		data := KISSUnescape(frame[2 : len(frame)-1])
		if len(data) != 2 {
			t.Fatalf("expected 2 data bytes, got %d", len(data))
		}
		got := int(data[0])<<8 | int(data[1])
		if got != 1550 {
			t.Fatalf("expected 1550 (15.5%%*100), got %d", got)
		}
	})

	t.Run("nil value", func(t *testing.T) {
		t.Parallel()
		frame := RNodeSetSTALock(nil)
		if frame != nil {
			t.Fatalf("expected nil frame for nil percentage, got %v", frame)
		}
	})
}

func TestRNodeSetLTALock(t *testing.T) {
	t.Parallel()
	t.Run("non-nil value", func(t *testing.T) {
		t.Parallel()
		pct := 50.0
		frame := RNodeSetLTALock(&pct)
		if frame == nil {
			t.Fatal("expected non-nil frame for non-nil percentage")
		}
		if frame[1] != KISSCmdLTALock {
			t.Fatalf("command byte must be CMD_LT_ALOCK (0x0C), got 0x%02X", frame[1])
		}
		data := KISSUnescape(frame[2 : len(frame)-1])
		got := int(data[0])<<8 | int(data[1])
		if got != 5000 {
			t.Fatalf("expected 5000 (50.0%%*100), got %d", got)
		}
	})

	t.Run("nil value", func(t *testing.T) {
		t.Parallel()
		frame := RNodeSetLTALock(nil)
		if frame != nil {
			t.Fatalf("expected nil frame for nil percentage, got %v", frame)
		}
	})
}

func TestRNodeSetRadioState(t *testing.T) {
	t.Parallel()
	for _, state := range []struct {
		name  string
		value byte
	}{
		{"off", RadioStateOff},
		{"on", RadioStateOn},
		{"ask", RadioStateAsk},
	} {
		t.Run(state.name, func(t *testing.T) {
			t.Parallel()
			frame := RNodeSetRadioState(state.value)
			if frame[1] != KISSCmdRadioState {
				t.Fatalf("command byte must be CMD_RADIO_STATE (0x06), got 0x%02X", frame[1])
			}
			data := KISSUnescape(frame[2 : len(frame)-1])
			if len(data) != 1 || data[0] != state.value {
				t.Fatalf("expected state 0x%02X, got %v", state.value, data)
			}
		})
	}
}

func TestRNodeValidateRadioState(t *testing.T) {
	t.Parallel()
	t.Run("all match", func(t *testing.T) {
		t.Parallel()
		err := RNodeValidateRadioState(
			433050000, 125000, 17, 7, 5, 1,
			433050000, 125000, 17, 7, 5, 1,
		)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	})

	t.Run("frequency within tolerance", func(t *testing.T) {
		t.Parallel()
		err := RNodeValidateRadioState(
			433050000, 125000, 17, 7, 5, 1,
			433050050, 125000, 17, 7, 5, 1,
		)
		if err != nil {
			t.Fatalf("frequency within 100Hz tolerance should pass, got %v", err)
		}
	})

	t.Run("frequency outside tolerance", func(t *testing.T) {
		t.Parallel()
		err := RNodeValidateRadioState(
			433050000, 125000, 17, 7, 5, 1,
			433051000, 125000, 17, 7, 5, 1,
		)
		if err == nil {
			t.Fatal("expected frequency mismatch error")
		}
	})

	t.Run("bandwidth mismatch", func(t *testing.T) {
		t.Parallel()
		err := RNodeValidateRadioState(
			433050000, 125000, 17, 7, 5, 1,
			433050000, 250000, 17, 7, 5, 1,
		)
		if err == nil {
			t.Fatal("expected bandwidth mismatch error")
		}
	})

	t.Run("tx power mismatch", func(t *testing.T) {
		t.Parallel()
		err := RNodeValidateRadioState(
			433050000, 125000, 17, 7, 5, 1,
			433050000, 125000, 20, 7, 5, 1,
		)
		if err == nil {
			t.Fatal("expected tx power mismatch error")
		}
	})

	t.Run("spreading factor mismatch", func(t *testing.T) {
		t.Parallel()
		err := RNodeValidateRadioState(
			433050000, 125000, 17, 7, 5, 1,
			433050000, 125000, 17, 12, 5, 1,
		)
		if err == nil {
			t.Fatal("expected spreading factor mismatch error")
		}
	})

	t.Run("coding rate mismatch", func(t *testing.T) {
		t.Parallel()
		err := RNodeValidateRadioState(
			433050000, 125000, 17, 7, 5, 1,
			433050000, 125000, 17, 7, 8, 1,
		)
		if err == nil {
			t.Fatal("expected coding rate mismatch error")
		}
	})

	t.Run("radio state mismatch", func(t *testing.T) {
		t.Parallel()
		err := RNodeValidateRadioState(
			433050000, 125000, 17, 7, 5, RadioStateOn,
			433050000, 125000, 17, 7, 5, RadioStateOff,
		)
		if err == nil {
			t.Fatal("expected radio state mismatch error")
		}
	})

	t.Run("zero reported values skip numeric validation", func(t *testing.T) {
		t.Parallel()
		err := RNodeValidateRadioState(
			433050000, 125000, 17, 7, 5, RadioStateOn,
			0, 0, 0, 0, 0, RadioStateOn,
		)
		if err != nil {
			t.Fatalf("zero numeric values should skip validation, got %v", err)
		}
	})
}

func TestRNodeUpdateBitrate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		sf          int
		cr          int
		bandwidthHz int
		wantApprox  int
		tolerance   int
	}{
		{"sf7_cr5_125kHz", 7, 5, 125000, 5468, 10},
		{"sf12_cr8_125kHz", 12, 8, 125000, 183, 1},
		{"sf7_cr5_250kHz", 7, 5, 250000, 10937, 20},
		{"zero_sf", 0, 5, 125000, 0, 0},
		{"zero_cr", 7, 0, 125000, 0, 0},
		{"zero_bw", 7, 5, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RNodeUpdateBitrate(tt.sf, tt.cr, tt.bandwidthHz)
			if tt.tolerance == 0 {
				if got != tt.wantApprox {
					t.Fatalf("RNodeUpdateBitrate(%d, %d, %d) = %d, want %d", tt.sf, tt.cr, tt.bandwidthHz, got, tt.wantApprox)
				}
				return
			}
			diff := got - tt.wantApprox
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.tolerance {
				t.Fatalf("RNodeUpdateBitrate(%d, %d, %d) = %d, want approx %d (tolerance %d)", tt.sf, tt.cr, tt.bandwidthHz, got, tt.wantApprox, tt.tolerance)
			}
		})
	}
}

func TestRNodeFrameRoundtrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		frame []byte
		cmd   byte
	}{
		{"frequency", RNodeSetFrequency(868000000), KISSCmdFrequency},
		{"bandwidth", RNodeSetBandwidth(500000), KISSCmdBandwidth},
		{"txpower", RNodeSetTXPower(23), KISSCmdTXPower},
		{"spreading_factor", RNodeSetSpreadingFactor(10), KISSCmdSF},
		{"coding_rate", RNodeSetCodingRate(6), KISSCmdCR},
		{"radio_state_on", RNodeSetRadioState(RadioStateOn), KISSCmdRadioState},
		{"radio_state_off", RNodeSetRadioState(RadioStateOff), KISSCmdRadioState},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.frame[0] != KISSFend || tt.frame[len(tt.frame)-1] != KISSFend {
				t.Fatal("frame must start and end with FEND")
			}
			if tt.frame[1] != tt.cmd {
				t.Fatalf("command byte must be 0x%02X, got 0x%02X", tt.cmd, tt.frame[1])
			}
			data := KISSUnescape(tt.frame[2 : len(tt.frame)-1])
			if len(data) == 0 {
				t.Fatal("frame data must not be empty after unescape")
			}
			_ = data
		})
	}
}

func TestRNodeKISSConstants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  byte
		want byte
	}{
		{"CMD_FREQUENCY", KISSCmdFrequency, 0x01},
		{"CMD_BANDWIDTH", KISSCmdBandwidth, 0x02},
		{"CMD_TXPOWER", KISSCmdTXPower, 0x03},
		{"CMD_SF", KISSCmdSF, 0x04},
		{"CMD_CR", KISSCmdCR, 0x05},
		{"CMD_RADIO_STATE", KISSCmdRadioState, 0x06},
		{"CMD_RADIO_LOCK", KISSCmdRadioLock, 0x07},
		{"CMD_DETECT", KISSCmdDetect, 0x08},
		{"CMD_LEAVE", KISSCmdLeave, 0x0A},
		{"CMD_ST_ALOCK", KISSCmdSTALock, 0x0B},
		{"CMD_LT_ALOCK", KISSCmdLTALock, 0x0C},
		{"CMD_READY", KISSCmdReady, 0x0F},
		{"CMD_SEL_INT", KISSCmdSelInt, 0x1F},
		{"RadioStateOff", RadioStateOff, 0x00},
		{"RadioStateOn", RadioStateOn, 0x01},
		{"RadioStateAsk", RadioStateAsk, 0xFF},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.got != tt.want {
				t.Fatalf("expected %s = 0x%02X, got 0x%02X", tt.name, tt.want, tt.got)
			}
		})
	}
}

func TestRNodeSetSTALockSelectInt(t *testing.T) {
	t.Parallel()
	pct := 25.0
	frame := RNodeSetSTALockSelectInt(&pct, 3)
	if frame[1] != KISSCmdSelInt || frame[2] != 3 {
		t.Fatal("select prefix must select sub-interface index 3")
	}
	nil_frame := RNodeSetSTALockSelectInt(nil, 3)
	if nil_frame != nil {
		t.Fatal("nil percentage must return nil")
	}
}

func TestRNodeSetLTALockSelectInt(t *testing.T) {
	t.Parallel()
	pct := 10.0
	frame := RNodeSetLTALockSelectInt(&pct, 1)
	if frame[1] != KISSCmdSelInt || frame[2] != 1 {
		t.Fatal("select prefix must select sub-interface index 1")
	}
	nil_frame := RNodeSetLTALockSelectInt(nil, 1)
	if nil_frame != nil {
		t.Fatal("nil percentage must return nil")
	}
}

func TestRNodeSetRadioStateSelectInt(t *testing.T) {
	t.Parallel()
	frame := RNodeSetRadioStateSelectInt(RadioStateOn, 2)
	if !bytes.HasPrefix(frame[0:4], []byte{KISSFend, KISSCmdSelInt, 2, KISSFend}) {
		t.Fatalf("select prefix must be [FEND][CMD_SEL_INT][2][FEND], got %x", frame[:4])
	}
}

func TestRNodeSetSpreadingFactorSelectInt(t *testing.T) {
	t.Parallel()
	frame := RNodeSetSpreadingFactorSelectInt(9, 0)
	if frame[1] != KISSCmdSelInt || frame[2] != 0 {
		t.Fatal("select prefix must select sub-interface index 0")
	}
}

func TestRNodeSetCodingRateSelectInt(t *testing.T) {
	t.Parallel()
	frame := RNodeSetCodingRateSelectInt(7, 4)
	if frame[1] != KISSCmdSelInt || frame[2] != 4 {
		t.Fatal("select prefix must select sub-interface index 4")
	}
}

func TestRNodeSetBandwidthSelectInt(t *testing.T) {
	t.Parallel()
	frame := RNodeSetBandwidthSelectInt(250000, 5)
	if frame[1] != KISSCmdSelInt || frame[2] != 5 {
		t.Fatal("select prefix must select sub-interface index 5")
	}
}
