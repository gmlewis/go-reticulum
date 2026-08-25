// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

package interfaces

import (
	"bytes"
	"testing"
)

// be32 returns a 4-byte big-endian encoding of v.
func be32(v uint32) []byte { return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }

// be16 returns a 2-byte big-endian encoding of v.
func be16(v uint16) []byte { return []byte{byte(v >> 8), byte(v)} }

// feedFrame feeds every byte of a KISS frame through the decoder. It discards
// delivered data and errors (use feedFrameCheck for the cases that assert on
// them), keeping the common "drive a frame in and inspect state" callsites
// errcheck-clean.
func feedFrame(d *rnodeDecoder, frame []byte) {
	for _, b := range frame {
		_, _ = d.feed(b)
	}
}

// feedFrameCheck feeds a frame and returns the delivered CMD_DATA payload and
// the first fatal error, for the tests that assert on them.
func feedFrameCheck(d *rnodeDecoder, frame []byte) ([]byte, error) {
	var out []byte
	var ferr error
	for _, b := range frame {
		data, err := d.feed(b)
		if data != nil {
			out = append(out, data...)
		}
		if err != nil && ferr == nil {
			ferr = err
		}
	}
	return out, ferr
}

// TestRNodeDecoderLiveRadioTelemetry feeds the inbound frames the connected
// Heltec LoRa32 RNode actually emits (captured from a live Python rnsd run
// against the radio) and asserts the Go decoder reproduces Python's reported
// state exactly: frequency 915.0 MHz, bandwidth 125.0 KHz, TX 17 dBm, SF8,
// CR5, on-air bitrate 3.12 kbps, symbol time 2.05ms (488 baud), preamble 18
// symbols (37ms), CSMA slot 24ms, DIFS 48ms, detect, firmware 1.85.
func TestRNodeDecoderLiveRadioTelemetry(t *testing.T) {
	t.Parallel()
	s := &rnodeRadioState{frequency: 915000000, bandwidth: 125000, txpower: 17, sf: 8, cr: 5, state: RadioStateOn}
	d := newRNodeDecoder(s, 508)

	feedFrame(d, KISSFrame(KISSCmdDetect, []byte{KISSDetectResp}))
	feedFrame(d, KISSFrame(KISSCmdFwVersion, []byte{1, 85}))
	feedFrame(d, KISSFrame(KISSCmdPlatform, []byte{0x80})) // ESP32
	feedFrame(d, KISSFrame(KISSCmdMcu, []byte{0x01}))
	feedFrame(d, KISSFrame(KISSCmdFrequency, be32(915000000)))
	feedFrame(d, KISSFrame(KISSCmdBandwidth, be32(125000)))
	feedFrame(d, KISSFrame(KISSCmdTXPower, []byte{17}))
	feedFrame(d, KISSFrame(KISSCmdSF, []byte{8}))
	feedFrame(d, KISSFrame(KISSCmdCR, []byte{5}))
	feedFrame(d, KISSFrame(KISSCmdRadioState, []byte{RadioStateOn}))
	feedFrame(d, KISSFrame(KISSCmdRadioLock, []byte{0x01}))
	// PHYPRM: lst=2050 lsr=488 prs=18 prt=37 cst=24 dft=48 (live radio values)
	phyprm := append(be16(2050), be16(488)...)
	phyprm = append(phyprm, be16(18)...)
	phyprm = append(phyprm, be16(37)...)
	phyprm = append(phyprm, be16(24)...)
	phyprm = append(phyprm, be16(48)...)
	feedFrame(d, KISSFrame(0x26, phyprm))

	if !s.detected {
		t.Fatal("detected should be true after DETECT_RESP")
	}
	if s.majVersion != 1 || s.minVersion != 85 {
		t.Fatalf("firmware version = %d.%d, want 1.85", s.majVersion, s.minVersion)
	}
	if !s.firmwareOK {
		t.Fatal("firmware 1.85 (>= 1.52) should validate OK")
	}
	if s.platform == nil || *s.platform != 0x80 {
		t.Fatalf("platform = %v, want 0x80 (ESP32)", s.platform)
	}
	if s.rFrequency == nil || *s.rFrequency != 915000000 {
		t.Fatalf("rFrequency = %v, want 915000000", s.rFrequency)
	}
	if s.rBandwidth == nil || *s.rBandwidth != 125000 {
		t.Fatalf("rBandwidth = %v, want 125000", s.rBandwidth)
	}
	if s.rTXPower == nil || *s.rTXPower != 17 {
		t.Fatalf("rTXPower = %v, want 17", s.rTXPower)
	}
	if s.rSF == nil || *s.rSF != 8 {
		t.Fatalf("rSF = %v, want 8", s.rSF)
	}
	if s.rCR == nil || *s.rCR != 5 {
		t.Fatalf("rCR = %v, want 5", s.rCR)
	}
	if s.bitrate != 3125 {
		t.Fatalf("bitrate = %d, want 3125 (3.12 kbps)", s.bitrate)
	}
	if s.rSymbolTimeMs == nil || *s.rSymbolTimeMs != 2.05 {
		t.Fatalf("rSymbolTimeMs = %v, want 2.05", s.rSymbolTimeMs)
	}
	if s.rSymbolRate == nil || *s.rSymbolRate != 488 {
		t.Fatalf("rSymbolRate = %v, want 488", s.rSymbolRate)
	}
	if s.rPreambleSymbols == nil || *s.rPreambleSymbols != 18 {
		t.Fatalf("rPreambleSymbols = %v, want 18", s.rPreambleSymbols)
	}
	if s.rPreambleTimeMs == nil || *s.rPreambleTimeMs != 37 {
		t.Fatalf("rPreambleTimeMs = %v, want 37", s.rPreambleTimeMs)
	}
	if s.rCSMASlotTimeMs == nil || *s.rCSMASlotTimeMs != 24 {
		t.Fatalf("rCSMASlotTimeMs = %v, want 24", s.rCSMASlotTimeMs)
	}
	if s.rCSMADifsMs == nil || *s.rCSMADifsMs != 48 {
		t.Fatalf("rCSMADifsMs = %v, want 48", s.rCSMADifsMs)
	}
	if !s.validateRadioState() {
		t.Fatal("validateRadioState should pass for matching reported params")
	}
}

// TestRNodeDecoderAllCommands covers every KISS command the radio can emit,
// asserting the decoder produces the intended values (Python's decode logic,
// NOT its latent Python-3 ord() bug on STAT_RX/TX).
func TestRNodeDecoderAllCommands(t *testing.T) {
	t.Parallel()

	t.Run("stat_rx_tx_escape", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		d := newRNodeDecoder(s, 508)
		// 0xC0 inside the payload must be KISS-escaped; decoded uint32 still 0x00C0DB01.
		feedFrame(d, KISSFrame(0x21, be32(0x00C0DB01)))
		feedFrame(d, KISSFrame(0x22, be32(654321)))
		if s.rStatRX == nil || *s.rStatRX != 0x00C0DB01 {
			t.Fatalf("rStatRX = %v, want 0x00C0DB01", s.rStatRX)
		}
		if s.rStatTX == nil || *s.rStatTX != 654321 {
			t.Fatalf("rStatTX = %v, want 654321", s.rStatTX)
		}
	})

	t.Run("rssi_snr_quality", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{sf: 8}
		s.rSF = intPtr(8)
		d := newRNodeDecoder(s, 508)
		feedFrame(d, KISSFrame(0x23, []byte{0x9D})) // 157 -> 0 dBm
		feedFrame(d, KISSFrame(0x24, []byte{0x28})) // int8(40)=40 *0.25 = 10.0
		if s.rStatRSSI == nil || *s.rStatRSSI != 0 {
			t.Fatalf("rStatRSSI = %v, want 0", s.rStatRSSI)
		}
		if s.rStatSNR == nil || *s.rStatSNR != 10.0 {
			t.Fatalf("rStatSNR = %v, want 10.0", s.rStatSNR)
		}
		// sf=8 -> sfs=1; qMin=-9-2=-11; qMax=6; span=17; q=(10-(-11))/17*100=123.5.. -> clamp 100
		if s.rStatQ == nil || *s.rStatQ != 100.0 {
			t.Fatalf("rStatQ = %v, want 100.0 (clamped)", s.rStatQ)
		}
	})

	t.Run("snr_negative_quality", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		s.rSF = intPtr(7)
		d := newRNodeDecoder(s, 508)
		feedFrame(d, KISSFrame(0x24, []byte{0xE8})) // int8(232)=-24 *0.25 = -6.0
		if s.rStatSNR == nil || *s.rStatSNR != -6.0 {
			t.Fatalf("rStatSNR = %v, want -6.0", s.rStatSNR)
		}
		// sf=7 -> sfs=0; qMin=-9; span=15; q=(-6-(-9))/15*100=20.0
		if s.rStatQ == nil || *s.rStatQ != 20.0 {
			t.Fatalf("rStatQ = %v, want 20.0", s.rStatQ)
		}
	})

	t.Run("alocks", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		d := newRNodeDecoder(s, 508)
		feedFrame(d, KISSFrame(KISSCmdSTALock, be16(1550)))
		feedFrame(d, KISSFrame(KISSCmdLTALock, be16(3000)))
		if s.rStAlock == nil || *s.rStAlock != 15.5 {
			t.Fatalf("rStAlock = %v, want 15.5", s.rStAlock)
		}
		if s.rLtAlock == nil || *s.rLtAlock != 30.0 {
			t.Fatalf("rLtAlock = %v, want 30.0", s.rLtAlock)
		}
	})

	t.Run("chtm", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		d := newRNodeDecoder(s, 508)
		payload := append(be16(1200), be16(3400)...)
		payload = append(payload, be16(560)...)
		payload = append(payload, be16(780)...)
		payload = append(payload, []byte{0xA0, 0x96, 0xFF}...)
		feedFrame(d, KISSFrame(0x25, payload))
		if s.rAirtimeShort != 12.0 || s.rAirtimeLong != 34.0 {
			t.Fatalf("airtime = %v/%v, want 12/34", s.rAirtimeShort, s.rAirtimeLong)
		}
		if s.rChanLoadShort != 5.6 || s.rChanLoadLong != 7.8 {
			t.Fatalf("channel load = %v/%v, want 5.6/7.8", s.rChanLoadShort, s.rChanLoadLong)
		}
		if s.rCurrentRSSI == nil || *s.rCurrentRSSI != 0xA0-157 {
			t.Fatalf("rCurrentRSSI = %v, want %d", s.rCurrentRSSI, 0xA0-157)
		}
		if s.rNoiseFloor == nil || *s.rNoiseFloor != 0x96-157 {
			t.Fatalf("rNoiseFloor = %v, want %d", s.rNoiseFloor, 0x96-157)
		}
		if s.rInterference != nil {
			t.Fatalf("rInterference = %v, want nil (ntf=0xFF)", s.rInterference)
		}
	})

	t.Run("csma_bat_temp_random", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		d := newRNodeDecoder(s, 508)
		feedFrame(d, KISSFrame(0x28, []byte{0x02, 0x03, 0x05}))
		feedFrame(d, KISSFrame(0x27, []byte{0x02, 0x55}))
		feedFrame(d, KISSFrame(0x29, []byte{120 + 25}))
		feedFrame(d, KISSFrame(0x40, []byte{0xAB}))
		if s.rCSMACwBand == nil || *s.rCSMACwBand != 2 || *s.rCSMACwMin != 3 || *s.rCSMACwMax != 5 {
			t.Fatalf("csma = %v/%v/%v, want 2/3/5", s.rCSMACwBand, s.rCSMACwMin, s.rCSMACwMax)
		}
		if s.rBatteryState == nil || *s.rBatteryState != 2 || s.rBatteryPercent == nil || *s.rBatteryPercent != 0x55 {
			t.Fatalf("battery = %v/%v, want 2/85", s.rBatteryState, s.rBatteryPercent)
		}
		if s.rTemperature == nil || *s.rTemperature != 25 {
			t.Fatalf("temperature = %v, want 25", s.rTemperature)
		}
		if s.rRandom == nil || *s.rRandom != 0xAB {
			t.Fatalf("random = %v, want 0xAB", s.rRandom)
		}
	})

	t.Run("framebuffer_display", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		d := newRNodeDecoder(s, 508)
		fb := bytes.Repeat([]byte{0x11}, 512)
		disp := bytes.Repeat([]byte{0x22}, 1024)
		feedFrame(d, KISSFrame(KISSCmdFBRead, fb))
		feedFrame(d, KISSFrame(KISSCmdDispRead, disp))
		if !bytes.Equal(s.rFramebuffer, fb) {
			t.Fatalf("framebuffer len = %d, want 512", len(s.rFramebuffer))
		}
		if !bytes.Equal(s.rDisp, disp) {
			t.Fatalf("display len = %d, want 1024", len(s.rDisp))
		}
	})

	t.Run("error_fatal", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		d := newRNodeDecoder(s, 508)
		if _, err := feedFrameCheck(d, KISSFrame(0x90, []byte{0x01})); err == nil {
			t.Fatal("ERROR_INITRADIO should be fatal")
		}
	})

	t.Run("error_nonfatal", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		d := newRNodeDecoder(s, 508)
		if _, err := feedFrameCheck(d, KISSFrame(0x90, []byte{0x05})); err != nil {
			t.Fatalf("ERROR_MEMORY_LOW should be non-fatal, got %v", err)
		}
		if _, err := feedFrameCheck(d, KISSFrame(0x90, []byte{0x06})); err != nil {
			t.Fatalf("ERROR_MODEM_TIMEOUT should be non-fatal, got %v", err)
		}
		if len(s.hwErrors) != 2 {
			t.Fatalf("hwErrors len = %d, want 2", len(s.hwErrors))
		}
	})

	t.Run("data_delivery", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		d := newRNodeDecoder(s, 508)
		payload := []byte("hello rnode data")
		out, err := feedFrameCheck(d, KISSFrame(KISSCmdData, payload))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !bytes.Equal(out, payload) {
			t.Fatalf("delivered %q, want %q", out, payload)
		}
	})

	t.Run("detect_negative", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{detected: true}
		d := newRNodeDecoder(s, 508)
		feedFrame(d, KISSFrame(KISSCmdDetect, []byte{0x00}))
		if s.detected {
			t.Fatal("non-DETECT_RESP byte should clear detected")
		}
	})

	t.Run("ready_signal", func(t *testing.T) {
		t.Parallel()
		s := &rnodeRadioState{}
		d := newRNodeDecoder(s, 508)
		feedFrame(d, KISSFrame(KISSCmdReady, []byte{0x00}))
		if !s.readySignal {
			t.Fatal("CMD_READY should set readySignal")
		}
	})
}

// TestRNodeValidateRadioStateMismatch exercises the validation tolerance.
func TestRNodeValidateRadioStateMismatch(t *testing.T) {
	t.Parallel()
	s := &rnodeRadioState{frequency: 915000000, bandwidth: 125000, txpower: 17, sf: 8, state: RadioStateOn}
	rf := 915000100
	s.rFrequency = &rf
	rb := 125000
	s.rBandwidth = &rb
	rt := 17
	s.rTXPower = &rt
	rsf := 8
	s.rSF = &rsf
	rst := byte(RadioStateOn)
	s.rState = &rst
	if !s.validateRadioState() {
		t.Fatal("freq within 100Hz tolerance should validate")
	}
	bad := 915000500
	s.rFrequency = &bad
	if s.validateRadioState() {
		t.Fatal("freq >100Hz off should fail validation")
	}
}
