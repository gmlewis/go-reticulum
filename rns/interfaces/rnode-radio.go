// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import "fmt"

// rNodeFreqToleranceHz is the maximum allowable deviation in Hz between the
// configured and reported radio frequency before validation marks the state
// as invalid.
const rNodeFreqToleranceHz = 100

// RNodeSetFrequency builds a KISS command frame to configure the radio
// frequency to the given value in Hz.
func RNodeSetFrequency(frequencyHz uint32) []byte {
	return KISSFrameUint32(KISSCmdFrequency, frequencyHz)
}

// RNodeSetFrequencySelectInt builds a KISS command pair to configure the radio
// frequency on the specified sub-interface index.
func RNodeSetFrequencySelectInt(frequencyHz uint32, index byte) []byte {
	data := []byte{
		byte(frequencyHz >> 24),
		byte(frequencyHz >> 16),
		byte(frequencyHz >> 8),
		byte(frequencyHz),
	}
	return KISSFrameSelectInt(KISSCmdFrequency, index, data)
}

// RNodeSetBandwidth builds a KISS command frame to configure the radio
// bandwidth to the given value in Hz.
func RNodeSetBandwidth(bandwidthHz uint32) []byte {
	return KISSFrameUint32(KISSCmdBandwidth, bandwidthHz)
}

// RNodeSetBandwidthSelectInt builds a KISS command pair to configure the
// radio bandwidth on the specified sub-interface index.
func RNodeSetBandwidthSelectInt(bandwidthHz uint32, index byte) []byte {
	data := []byte{
		byte(bandwidthHz >> 24),
		byte(bandwidthHz >> 16),
		byte(bandwidthHz >> 8),
		byte(bandwidthHz),
	}
	return KISSFrameSelectInt(KISSCmdBandwidth, index, data)
}

// RNodeSetTXPower builds a KISS command frame to configure transmit power.
// The value is sent as a single unsigned byte (range 0–37 dBm).
func RNodeSetTXPower(txPowerDBm byte) []byte {
	return KISSFrameUint8(KISSCmdTXPower, txPowerDBm)
}

// RNodeSetTXPowerSigned builds a KISS command frame to configure transmit
// power using a signed byte value. Multi-interface variants use signed
// representation (range -9 to 37 dBm).
func RNodeSetTXPowerSigned(txPowerDBm int8) []byte {
	return KISSFrameUint8(KISSCmdTXPower, byte(txPowerDBm))
}

// RNodeSetTXPowerSelectInt builds a KISS command pair to configure transmit
// power on the specified sub-interface index using a signed byte.
func RNodeSetTXPowerSelectInt(txPowerDBm int8, index byte) []byte {
	return KISSFrameSelectInt(KISSCmdTXPower, index, []byte{byte(txPowerDBm)})
}

// RNodeSetSpreadingFactor builds a KISS command frame to configure the LoRa
// spreading factor. Valid values are 5–12.
func RNodeSetSpreadingFactor(sf byte) []byte {
	return KISSFrameUint8(KISSCmdSF, sf)
}

// RNodeSetSpreadingFactorSelectInt builds a KISS command pair to configure
// the spreading factor on the specified sub-interface index.
func RNodeSetSpreadingFactorSelectInt(sf byte, index byte) []byte {
	return KISSFrameSelectInt(KISSCmdSF, index, []byte{sf})
}

// RNodeSetCodingRate builds a KISS command frame to configure the LoRa coding
// rate. Valid values are 5–8 (corresponding to 4/5 through 4/8).
func RNodeSetCodingRate(cr byte) []byte {
	return KISSFrameUint8(KISSCmdCR, cr)
}

// RNodeSetCodingRateSelectInt builds a KISS command pair to configure the
// coding rate on the specified sub-interface index.
func RNodeSetCodingRateSelectInt(cr byte, index byte) []byte {
	return KISSFrameSelectInt(KISSCmdCR, index, []byte{cr})
}

// RNodeSetSTALock builds a KISS command frame to configure the short-term
// airtime lock. The percentage (0.0–100.0) is multiplied by 100 and sent
// as a 16-bit big-endian value. If alockPercent is nil, nil is returned
// (no-op).
func RNodeSetSTALock(alockPercent *float64) []byte {
	if alockPercent == nil {
		return nil
	}
	at := uint16(*alockPercent * 100)
	return KISSFrameUint16(KISSCmdSTALock, at)
}

// RNodeSetSTALockSelectInt builds a KISS command pair to configure the
// short-term airtime lock on the specified sub-interface index.
func RNodeSetSTALockSelectInt(alockPercent *float64, index byte) []byte {
	if alockPercent == nil {
		return nil
	}
	at := uint16(*alockPercent * 100)
	data := []byte{byte(at >> 8), byte(at)}
	return KISSFrameSelectInt(KISSCmdSTALock, index, data)
}

// RNodeSetLTALock builds a KISS command frame to configure the long-term
// airtime lock. The percentage (0.0–100.0) is multiplied by 100 and sent
// as a 16-bit big-endian value. If ltaPercent is nil, nil is returned
// (no-op).
func RNodeSetLTALock(ltaPercent *float64) []byte {
	if ltaPercent == nil {
		return nil
	}
	at := uint16(*ltaPercent * 100)
	return KISSFrameUint16(KISSCmdLTALock, at)
}

// RNodeSetLTALockSelectInt builds a KISS command pair to configure the
// long-term airtime lock on the specified sub-interface index.
func RNodeSetLTALockSelectInt(ltaPercent *float64, index byte) []byte {
	if ltaPercent == nil {
		return nil
	}
	at := uint16(*ltaPercent * 100)
	data := []byte{byte(at >> 8), byte(at)}
	return KISSFrameSelectInt(KISSCmdLTALock, index, data)
}

// RNodeSetRadioState builds a KISS command frame to change the radio state.
// Use RadioStateOff, RadioStateOn, or RadioStateAsk.
func RNodeSetRadioState(state byte) []byte {
	return KISSFrameUint8(KISSCmdRadioState, state)
}

// RNodeSetRadioStateSelectInt builds a KISS command pair to change the radio
// state on the specified sub-interface index.
func RNodeSetRadioStateSelectInt(state byte, index byte) []byte {
	return KISSFrameSelectInt(KISSCmdRadioState, index, []byte{state})
}

// RNodeValidateRadioState checks whether the reported radio state matches the
// configured parameters within tolerance. Frequency tolerance is ±100 Hz.
// All other parameters must match exactly. Returns an error describing the
// first mismatch found, or nil if the state is valid.
func RNodeValidateRadioState(configuredFreq, configuredBW, configuredTXPower, configuredSF, configuredCR, configuredState int, reportedFreq, reportedBW, reportedTXPower, reportedSF, reportedCR, reportedState int) error {
	if reportedFreq != 0 && absInt(configuredFreq-reportedFreq) > rNodeFreqToleranceHz {
		return fmt.Errorf("frequency mismatch: configured %d Hz, reported %d Hz", configuredFreq, reportedFreq)
	}
	if reportedBW != 0 && configuredBW != reportedBW {
		return fmt.Errorf("bandwidth mismatch: configured %d Hz, reported %d Hz", configuredBW, reportedBW)
	}
	if reportedTXPower != 0 && configuredTXPower != reportedTXPower {
		return fmt.Errorf("tx power mismatch: configured %d dBm, reported %d dBm", configuredTXPower, reportedTXPower)
	}
	if reportedSF != 0 && configuredSF != reportedSF {
		return fmt.Errorf("spreading factor mismatch: configured %d, reported %d", configuredSF, reportedSF)
	}
	if reportedCR != 0 && configuredCR != reportedCR {
		return fmt.Errorf("coding rate mismatch: configured %d, reported %d", configuredCR, reportedCR)
	}
	if configuredState != reportedState {
		return fmt.Errorf("radio state mismatch: configured %d, reported %d", configuredState, reportedState)
	}
	return nil
}

// RNodeUpdateBitrate calculates the estimated bitrate from the given radio
// parameters using the LoRa data rate formula:
//
//	bitrate = sf * (4/cr) / (2^sf / (bandwidth/1000)) * 1000
//
// which simplifies to: bitrate = sf * 4 * bandwidth / (cr * 2^sf).
// All parameters use their integer representations (sf=7..12, cr=5..8,
// bandwidth in Hz). The result is in bits per second.
func RNodeUpdateBitrate(sf, cr, bandwidthHz int) int {
	if sf <= 0 || cr <= 0 || bandwidthHz <= 0 {
		return 0
	}
	denominator := cr * (1 << sf)
	if denominator == 0 {
		return 0
	}
	bitrate := float64(sf) * 4.0 * float64(bandwidthHz) / float64(denominator)
	return int(bitrate)
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
