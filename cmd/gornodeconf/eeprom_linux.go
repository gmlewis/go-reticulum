// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"crypto/md5"
	"errors"
	"fmt"
	"math"
	"time"
)

type eepromDownloaderWriter interface {
	Write([]byte) (int, error)
}

type eepromDownloaderSleeper interface {
	Sleep(time.Duration)
}

type eepromDownloaderState struct {
	name           string
	eeprom         []byte
	cfgSector      []byte
	provisioned    bool
	configured     bool
	product        byte
	model          byte
	hwRev          byte
	serialno       []byte
	made           []byte
	checksum       []byte
	signature      []byte
	minFreq        int
	maxFreq        int
	maxOutput      int
	confSF         byte
	confCR         byte
	confTXPower    byte
	confFrequency  int
	confBandwidth  int
	vendor         string
	locallySigned  bool
	signatureValid bool
	version        string
	writer         eepromDownloaderWriter
	sleeper        eepromDownloaderSleeper
	parse          func() error
}

func (s *eepromDownloaderState) downloadEEPROM() error {
	s.eeprom = nil
	command := []byte{kissFend, 0x51, 0x00, kissFend}
	written, err := s.writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return errors.New("An IO error occurred while downloading EEPROM")
	}

	s.sleeper.Sleep(600 * time.Millisecond)
	if s.eeprom == nil {
		return errors.New("Could not download EEPROM from device. Is a valid firmware installed?")
	}
	if s.parse != nil {
		return s.parse()
	}
	return nil
}

func (s *eepromDownloaderState) downloadCfgSector() error {
	s.cfgSector = nil
	command := []byte{kissFend, 0x6d, 0x00, kissFend}
	written, err := s.writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return errors.New("An IO error occurred while downloading config sector")
	}

	s.sleeper.Sleep(600 * time.Millisecond)
	return nil
}

func (s *eepromDownloaderState) parseEEPROM() error {
	if len(s.eeprom) == 0 {
		return errors.New("Invalid EEPROM data, could not parse device EEPROM.")
	}
	if s.eeprom[0x9b] != 0x73 {
		s.provisioned = false
		return errors.New("EEPROM is invalid, no further information available")
	}

	s.provisioned = true
	s.product = s.eeprom[0x00]
	s.model = s.eeprom[0x01]
	s.hwRev = s.eeprom[0x02]
	s.serialno = append([]byte(nil), s.eeprom[0x03:0x07]...)
	s.made = append([]byte(nil), s.eeprom[0x07:0x0b]...)
	s.checksum = append([]byte(nil), s.eeprom[0x0b:0x1b]...)
	s.signature = append([]byte(nil), s.eeprom[0x1b:0x9b]...)

	if info, ok := modelCapabilitiesByCode[s.model]; ok {
		s.minFreq = info.minFreq
		s.maxFreq = info.maxFreq
		s.maxOutput = info.maxOutput
	} else {
		s.minFreq = 0
		s.maxFreq = 0
		s.maxOutput = 0
	}

	checksummedInfo := []byte{s.product, s.model, s.hwRev}
	checksummedInfo = append(checksummedInfo, s.serialno...)
	checksummedInfo = append(checksummedInfo, s.made...)
	digest := md5.Sum(checksummedInfo)
	if !bytesEqual(s.checksum, digest[:]) {
		s.provisioned = false
		return errors.New("EEPROM checksum mismatch")
	}

	if s.eeprom[0xa7] == 0x73 {
		s.configured = true
		s.confSF = s.eeprom[0x9c]
		s.confCR = s.eeprom[0x9d]
		s.confTXPower = s.eeprom[0x9e]
		s.confBandwidth = int(s.eeprom[0x9f])<<24 | int(s.eeprom[0xa0])<<16 | int(s.eeprom[0xa1])<<8 | int(s.eeprom[0xa2])
		s.confFrequency = int(s.eeprom[0xa3])<<24 | int(s.eeprom[0xa4])<<16 | int(s.eeprom[0xa5])<<8 | int(s.eeprom[0xa6])
	} else {
		s.configured = false
	}

	return nil
}

func (s *eepromDownloaderState) deviceInfoLines(timestring string) []string {
	sigstring := "Unverified"
	if s.signatureValid {
		if s.locallySigned {
			sigstring = "Validated - Local signature"
		} else {
			sigstring = "Genuine board, vendor is " + s.vendor
		}
	}

	productName := productNames[s.product]
	modelInfo := modelCapabilitiesByCode[s.model]

	lines := []string{
		"",
		"Device info:",
		fmt.Sprintf("\tProduct            : %v %v (%02x:%02x)", productName, modelInfo.summary, s.product, s.model),
		"\tDevice signature   : " + sigstring,
		"\tFirmware version   : " + s.version,
		fmt.Sprintf("\tHardware revision  : %v", int(s.hwRev)),
		"\tSerial number      : " + hexBytes(s.serialno),
		"\tModem chip         : " + modelInfo.modem,
		fmt.Sprintf("\tFrequency range    : %v MHz - %v MHz", float64(s.minFreq)/1e6, float64(s.maxFreq)/1e6),
		fmt.Sprintf("\tMax TX power       : %v dBm", s.maxOutput),
		"\tManufactured       : " + timestring,
	}

	if s.configured {
		txpMw := math.Round(math.Pow(10, float64(s.confTXPower)/10.0)*1000) / 1000
		lines = append(lines,
			"",
			"\tDevice mode        : TNC",
			fmt.Sprintf("\t  Frequency        : %v MHz", float64(s.confFrequency)/1000000.0),
			fmt.Sprintf("\t  Bandwidth        : %v KHz", float64(s.confBandwidth)/1000.0),
			fmt.Sprintf("\t  TX power         : %v dBm (%v mW)", s.confTXPower, txpMw),
			fmt.Sprintf("\t  Spreading factor : %v", s.confSF),
			fmt.Sprintf("\t  Coding rate      : %v", s.confCR),
			fmt.Sprintf("\t  On-air bitrate   : %v kbps", s.onAirBitrateKbps()),
		)
	} else {
		lines = append(lines, "\tDevice mode        : Normal (host-controlled)")
	}

	return lines
}

func (s *eepromDownloaderState) onAirBitrateKbps() float64 {
	if s.confBandwidth == 0 || s.confSF == 0 || s.confCR == 0 {
		return 0
	}
	bitrate := float64(s.confSF) * ((4.0 / float64(s.confCR)) / (math.Pow(2, float64(s.confSF)) / (float64(s.confBandwidth) / 1000.0))) * 1000
	return math.Round((bitrate/1000.0)*100) / 100
}

type modelCapability struct {
	minFreq   int
	maxFreq   int
	maxOutput int
	summary   string
	modem     string
}

var modelCapabilitiesByCode = map[byte]modelCapability{
	0xa4: {minFreq: 410000000, maxFreq: 525000000, maxOutput: 14, summary: "410 - 525 MHz", modem: "SX1278"},
}

var productNames = map[byte]string{
	0x03: "RNode",
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func hexBytes(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	result := make([]byte, 0, len(data)*2)
	for _, value := range data {
		result = append(result, "0123456789abcdef"[value>>4], "0123456789abcdef"[value&0x0f])
	}
	return string(result)
}
