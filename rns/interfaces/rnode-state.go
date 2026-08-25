// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

package interfaces

import "fmt"

// rnodeRSSIOffset is the RSSI offset subtracted from the raw radio RSSI/CHTM
// bytes to yield dBm (RNS/Interfaces/RNodeInterface.py: RSSI_OFFSET = 157).
const rnodeRSSIOffset = 157

// rnodeQSNRMinBase/Max/Step define the SNR→link-quality mapping
// (RNodeInterface.py: Q_SNR_MIN_BASE=-9, Q_SNR_MAX=6, Q_SNR_STEP=2).
const (
	rnodeQSNRMinBase = -9.0
	rnodeQSNRMax     = 6.0
	rnodeQSNRStep    = 2.0
)

// rnodeFirmwareRequiredMaj/Min is the minimum firmware version the host
// accepts (RNodeInterface.py: REQUIRED_FW_VER_MAJ=1, REQUIRED_FW_VER_MIN=52).
const (
	rnodeFirmwareRequiredMaj = 1
	rnodeFirmwareRequiredMin = 52
)

// rnodeRadioState holds the configured and reported radio parameters for an
// RNode interface. It mirrors the attribute set of Python's RNodeInterface:
// configured values (frequency/bandwidth/txpower/sf/cr/state/alocks) and the
// reported values populated by the inbound KISS parser (r_*), plus detection
// and firmware state. Reported values use pointers so "not yet reported"
// (Python None) is distinguishable from a legitimate zero, which
// validateRadioState relies on.
type rnodeRadioState struct {
	// Configured (from the config file).
	frequency int
	bandwidth int
	txpower   int
	sf        int
	cr        int
	state     byte
	stAlock   *float64
	ltAlock   *float64

	// Reported by the radio (nil = not yet reported, matching Python None).
	rFrequency *int
	rBandwidth *int
	rTXPower   *int
	rSF        *int
	rCR        *int
	rState     *byte
	rLock      *byte

	majVersion int
	minVersion int
	firmwareOK bool

	detected bool
	platform *byte
	mcu      *byte
	display  bool

	bitrate int

	rStatRX        *int
	rStatTX        *int
	rStatRSSI      *int
	rStatSNR       *float64
	rStatQ         *float64
	rStAlock       *float64
	rLtAlock       *float64
	rAirtimeShort  float64
	rAirtimeLong   float64
	rChanLoadShort float64
	rChanLoadLong  float64

	rSymbolTimeMs    *float64
	rSymbolRate      *int
	rPreambleSymbols *int
	rPreambleTimeMs  *int
	rCSMASlotTimeMs  *int
	rCSMADifsMs      *int
	rCSMACwBand      *byte
	rCSMACwMin       *byte
	rCSMACwMax       *byte

	rCurrentRSSI  *int
	rNoiseFloor   *int
	rInterference *int

	rBatteryState   *byte
	rBatteryPercent *int
	rTemperature    *int
	rRandom         *byte

	rFramebuffer []byte
	rDisp        []byte

	// readySignal is set when CMD_READY arrives so the controller can flush
	// its packet queue (Python process_queue). The controller resets it.
	readySignal bool
	// hwErrors records non-fatal hardware errors (Python hw_errors list).
	hwErrors []rnodeHWError
}

// rnodeDecoder is the byte-by-byte KISS frame parser for an RNode's inbound
// serial stream. It is a faithful Go translation of
// RNodeInterface.readLoop (RNS/Interfaces/RNodeInterface.py), decoding every
// KISS command the radio emits into rnodeRadioState. It is pure with respect
// to the radio: feed bytes in, observe state changes and delivered data
// payloads out. This separation makes the parser unit-testable with golden
// byte sequences captured from Python.
type rnodeDecoder struct {
	state *rnodeRadioState
	hwmtu int

	inFrame       bool
	escape        bool
	command       byte
	dataBuffer    []byte
	commandBuffer []byte
}

// newRNodeDecoder returns a decoder bound to the given radio state.
func newRNodeDecoder(s *rnodeRadioState, hwmtu int) *rnodeDecoder {
	if hwmtu <= 0 {
		hwmtu = 508
	}
	return &rnodeDecoder{state: s, hwmtu: hwmtu, command: 0xFE}
}

// rnodeFatalError wraps an unrecoverable radio error (CMD_ERROR INITRADIO /
// TXFAILED / unknown) that, per Python, aborts the readLoop.
type rnodeFatalError struct{ Code byte }

func (e rnodeFatalError) Error() string {
	return fmt.Sprintf("rnode fatal hardware error (code 0x%02x)", e.Code)
}

// rnodeResetError signals a CMD_RESET 0xF8 on an online ESP32 (Python raises
// IOError "ESP32 reset" to trigger reinitialisation).
type rnodeResetError struct{}

func (e rnodeResetError) Error() string { return "ESP32 reset" }

// feed processes one inbound byte. It returns the delivered CMD_DATA payload
// (non-nil only when a complete data frame closed) and a fatal error if the
// radio reported an unrecoverable hardware error or an ESP32 reset. Mirrors
// the per-byte body of RNodeInterface.readLoop.
func (d *rnodeDecoder) feed(b byte) ([]byte, error) {
	if d.inFrame && b == KISSFend && d.command == KISSCmdData {
		d.inFrame = false
		payload := d.dataBuffer
		d.dataBuffer = nil
		d.commandBuffer = nil
		d.command = 0xFE
		return payload, nil
	}
	if b == KISSFend {
		d.inFrame = true
		d.command = 0xFE
		d.dataBuffer = nil
		d.commandBuffer = nil
		d.escape = false
		return nil, nil
	}
	if !d.inFrame || len(d.dataBuffer) >= d.hwmtu {
		return nil, nil
	}

	if len(d.dataBuffer) == 0 && d.command == 0xFE {
		d.command = b
		return nil, nil
	}

	switch d.command {
	case KISSCmdData:
		if b == KISSFesc {
			d.escape = true
			return nil, nil
		}
		if d.escape {
			d.escape = false
			switch b {
			case KISSTfend:
				b = KISSFend
			case KISSTfesc:
				b = KISSFesc
			}
		}
		d.dataBuffer = append(d.dataBuffer, b)
	case KISSCmdFrequency:
		if d.consumeEscaped(b, 4) {
			v := uint32(d.commandBuffer[0])<<24 | uint32(d.commandBuffer[1])<<16 | uint32(d.commandBuffer[2])<<8 | uint32(d.commandBuffer[3])
			i := int(v)
			d.state.rFrequency = &i
			d.state.updateBitrate()
		}
	case KISSCmdBandwidth:
		if d.consumeEscaped(b, 4) {
			v := uint32(d.commandBuffer[0])<<24 | uint32(d.commandBuffer[1])<<16 | uint32(d.commandBuffer[2])<<8 | uint32(d.commandBuffer[3])
			i := int(v)
			d.state.rBandwidth = &i
			d.state.updateBitrate()
		}
	case KISSCmdTXPower:
		d.state.rTXPower = intPtr(b)
	case KISSCmdSF:
		d.state.rSF = intPtr(b)
		d.state.updateBitrate()
	case KISSCmdCR:
		d.state.rCR = intPtr(b)
		d.state.updateBitrate()
	case KISSCmdRadioState:
		bb := b
		d.state.rState = &bb
	case KISSCmdRadioLock:
		bb := b
		d.state.rLock = &bb
	case KISSCmdFwVersion:
		if d.consumeEscaped(b, 2) {
			d.state.majVersion = int(d.commandBuffer[0])
			d.state.minVersion = int(d.commandBuffer[1])
			d.state.validateFirmware()
		}
	case 0x21: // CMD_STAT_RX
		if d.consumeEscaped(b, 4) {
			v := uint32(d.commandBuffer[0])<<24 | uint32(d.commandBuffer[1])<<16 | uint32(d.commandBuffer[2])<<8 | uint32(d.commandBuffer[3])
			i := int(v)
			d.state.rStatRX = &i
		}
	case 0x22: // CMD_STAT_TX
		if d.consumeEscaped(b, 4) {
			v := uint32(d.commandBuffer[0])<<24 | uint32(d.commandBuffer[1])<<16 | uint32(d.commandBuffer[2])<<8 | uint32(d.commandBuffer[3])
			i := int(v)
			d.state.rStatTX = &i
		}
	case 0x23: // CMD_STAT_RSSI
		i := int(b) - rnodeRSSIOffset
		d.state.rStatRSSI = &i
	case 0x24: // CMD_STAT_SNR
		snr := float64(int8(b)) * 0.25
		d.state.rStatSNR = &snr
		d.state.computeQuality()
	case KISSCmdSTALock:
		if d.consumeEscaped(b, 2) {
			at := uint16(d.commandBuffer[0])<<8 | uint16(d.commandBuffer[1])
			f := float64(at) / 100.0
			d.state.rStAlock = &f
		}
	case KISSCmdLTALock:
		if d.consumeEscaped(b, 2) {
			at := uint16(d.commandBuffer[0])<<8 | uint16(d.commandBuffer[1])
			f := float64(at) / 100.0
			d.state.rLtAlock = &f
		}
	case 0x25: // CMD_STAT_CHTM (11 bytes)
		if d.consumeEscaped(b, 11) {
			cb := d.commandBuffer
			ats := uint16(cb[0])<<8 | uint16(cb[1])
			atl := uint16(cb[2])<<8 | uint16(cb[3])
			cus := uint16(cb[4])<<8 | uint16(cb[5])
			cul := uint16(cb[6])<<8 | uint16(cb[7])
			crs := int(cb[8]) - rnodeRSSIOffset
			nfl := int(cb[9]) - rnodeRSSIOffset
			d.state.rAirtimeShort = float64(ats) / 100.0
			d.state.rAirtimeLong = float64(atl) / 100.0
			d.state.rChanLoadShort = float64(cus) / 100.0
			d.state.rChanLoadLong = float64(cul) / 100.0
			d.state.rCurrentRSSI = &crs
			d.state.rNoiseFloor = &nfl
			if cb[10] == 0xFF {
				d.state.rInterference = nil
			} else {
				v := int(cb[10]) - rnodeRSSIOffset
				d.state.rInterference = &v
			}
		}
	case 0x26: // CMD_STAT_PHYPRM (12 bytes)
		if d.consumeEscaped(b, 12) {
			cb := d.commandBuffer
			lst := float64(uint16(cb[0])<<8|uint16(cb[1])) / 1000.0
			lsr := int(uint16(cb[2])<<8 | uint16(cb[3]))
			prs := int(uint16(cb[4])<<8 | uint16(cb[5]))
			prt := int(uint16(cb[6])<<8 | uint16(cb[7]))
			cst := int(uint16(cb[8])<<8 | uint16(cb[9]))
			dft := int(uint16(cb[10])<<8 | uint16(cb[11]))
			d.state.rSymbolTimeMs = &lst
			d.state.rSymbolRate = &lsr
			d.state.rPreambleSymbols = &prs
			d.state.rPreambleTimeMs = &prt
			d.state.rCSMASlotTimeMs = &cst
			d.state.rCSMADifsMs = &dft
		}
	case 0x28: // CMD_STAT_CSMA (3 bytes)
		if d.consumeEscaped(b, 3) {
			cbw := d.commandBuffer[0]
			cbl := d.commandBuffer[1]
			cbh := d.commandBuffer[2]
			d.state.rCSMACwBand = &cbw
			d.state.rCSMACwMin = &cbl
			d.state.rCSMACwMax = &cbh
		}
	case 0x27: // CMD_STAT_BAT (2 bytes)
		if d.consumeEscaped(b, 2) {
			pct := min(max(int(d.commandBuffer[1]), 0), 100)
			st := d.commandBuffer[0]
			d.state.rBatteryState = &st
			d.state.rBatteryPercent = &pct
		}
	case 0x29: // CMD_STAT_TEMP (1 byte)
		if d.consumeEscaped(b, 1) {
			temp := int(d.commandBuffer[0]) - 120
			if temp < -30 || temp > 90 {
				d.state.rTemperature = nil
			} else {
				d.state.rTemperature = &temp
			}
		}
	case 0x40: // CMD_RANDOM
		bb := b
		d.state.rRandom = &bb
	case KISSCmdPlatform:
		bb := b
		d.state.platform = &bb
	case KISSCmdMcu:
		bb := b
		d.state.mcu = &bb
	case 0x90: // CMD_ERROR
		return nil, d.handleErrorCode(b)
	case KISSCmdReset:
		if b == 0xF8 {
			if d.state.platform != nil && *d.state.platform == 0x80 { // PLATFORM_ESP32
				return nil, rnodeResetError{}
			}
		}
	case KISSCmdReady:
		// CMD_READY signals the radio drained its TX queue; the controller
		// flushes the pending packet queue (process_queue). Signalled upward
		// by returning a sentinel via state — the controller polls it.
		d.state.readySignal = true
	case KISSCmdFBRead:
		if d.consumeEscaped(b, 512) {
			d.state.rFramebuffer = append([]byte(nil), d.commandBuffer...)
		}
	case KISSCmdDispRead:
		if d.consumeEscaped(b, 1024) {
			d.state.rDisp = append([]byte(nil), d.commandBuffer...)
		}
	case KISSCmdDetect:
		d.state.detected = b == KISSDetectResp
	}
	return nil, nil
}

// consumeEscaped accumulates one byte into the command buffer, applying KISS
// unescaping, and reports whether the buffer has reached the target length.
// Mirrors the shared escape/accumulate block repeated for each multi-byte
// command in RNodeInterface.readLoop.
func (d *rnodeDecoder) consumeEscaped(b byte, want int) bool {
	if b == KISSFesc {
		d.escape = true
		return false
	}
	if d.escape {
		d.escape = false
		switch b {
		case KISSTfend:
			b = KISSFend
		case KISSTfesc:
			b = KISSFesc
		}
	}
	d.commandBuffer = append(d.commandBuffer, b)
	return len(d.commandBuffer) == want
}

// handleErrorCode maps CMD_ERROR codes to Python's behaviour: INITRADIO and
// TXFAILED (and unknown codes) raise a fatal error; MEMORY_LOW and
// MODEM_TIMEOUT are recorded as non-fatal hardware errors.
func (d *rnodeDecoder) handleErrorCode(b byte) error {
	switch b {
	case 0x01: // ERROR_INITRADIO
		return rnodeFatalError{b}
	case 0x02: // ERROR_TXFAILED
		return rnodeFatalError{b}
	case 0x05: // ERROR_MEMORY_LOW
		d.state.hwErrors = append(d.state.hwErrors, rnodeHWError{Code: b, Desc: "Memory exhausted on connected device"})
		return nil
	case 0x06: // ERROR_MODEM_TIMEOUT
		d.state.hwErrors = append(d.state.hwErrors, rnodeHWError{Code: b, Desc: "Modem communication timed out on connected device"})
		return nil
	default:
		return rnodeFatalError{b}
	}
}

// rnodeHWError is a recorded non-fatal hardware error (Python hw_errors list).
type rnodeHWError struct {
	Code byte
	Desc string
}

// updateBitrate recomputes the on-air bitrate from the REPORTED radio
// parameters (RNodeInterface.updateBitrate):
//
//	bitrate = sf * ((4/cr) / (2^sf / (bw/1000))) * 1000
func (s *rnodeRadioState) updateBitrate() {
	if s.rSF == nil || s.rCR == nil || s.rBandwidth == nil {
		return
	}
	sf := *s.rSF
	cr := *s.rCR
	bw := *s.rBandwidth
	if sf <= 0 || cr <= 0 || bw <= 0 {
		s.bitrate = 0
		return
	}
	denom := float64(uint64(1) << sf)
	if denom == 0 {
		s.bitrate = 0
		return
	}
	s.bitrate = int(float64(sf) * (4.0 / float64(cr)) / (denom / (float64(bw) / 1000.0)) * 1000.0)
}

// computeQuality maps the reported SNR to a 0–100 link-quality percentage
// (RNodeInterface.readLoop CMD_STAT_SNR handler).
func (s *rnodeRadioState) computeQuality() {
	if s.rStatSNR == nil || s.rSF == nil {
		return
	}
	sfs := float64(*s.rSF - 7)
	qMin := rnodeQSNRMinBase - sfs*rnodeQSNRStep
	qMax := rnodeQSNRMax
	span := qMax - qMin
	if span == 0 {
		return
	}
	q := ((*s.rStatSNR - qMin) / span) * 100.0
	// Round to 1 decimal place (Python round(..., 1)).
	q = float64(int(q*10+0.5)) / 10.0
	// handle negatives correctly
	if q > 100.0 {
		q = 100.0
	}
	if q < 0.0 {
		q = 0.0
	}
	s.rStatQ = &q
}

// validateFirmware mirrors RNodeInterface.validate_firmware: firmware is OK
// when major > required, or major == required and minor >= required.
func (s *rnodeRadioState) validateFirmware() {
	if s.majVersion > rnodeFirmwareRequiredMaj {
		s.firmwareOK = true
		return
	}
	if s.majVersion == rnodeFirmwareRequiredMaj && s.minVersion >= rnodeFirmwareRequiredMin {
		s.firmwareOK = true
		return
	}
	s.firmwareOK = false
}

// validateRadioState mirrors RNodeInterface.validateRadioState: returns true
// when every reported parameter matches the configuration within tolerance
// (frequency ±100 Hz; bandwidth, txpower, sf, and radio state exact). A
// reported value that is still nil is skipped (Python treats None as "not yet
// reported, do not fail on it").
func (s *rnodeRadioState) validateRadioState() bool {
	ok := true
	if s.rFrequency != nil && absInt(s.frequency-*s.rFrequency) > 100 {
		ok = false
	}
	if s.rBandwidth != nil && s.bandwidth != *s.rBandwidth {
		ok = false
	}
	if s.rTXPower != nil && s.txpower != *s.rTXPower {
		ok = false
	}
	if s.rSF != nil && s.sf != *s.rSF {
		ok = false
	}
	if s.rState != nil && s.state != *s.rState {
		ok = false
	}
	return ok
}

func intPtr(v byte) *int { i := int(v); return &i }
