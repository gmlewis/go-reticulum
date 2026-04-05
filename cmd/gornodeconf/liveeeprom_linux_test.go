// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"testing"
	"time"
)

func validRnodeEEPROMFrame() []byte {
	eeprom := make([]byte, 0xa8)
	eeprom[0x00] = 0x03
	eeprom[0x01] = 0xa4
	eeprom[0x02] = 0x05
	eeprom[0x03] = 0x01
	eeprom[0x04] = 0x02
	eeprom[0x05] = 0x03
	eeprom[0x06] = 0x04
	eeprom[0x07] = 0x05
	eeprom[0x08] = 0x06
	eeprom[0x09] = 0x07
	eeprom[0x0a] = 0x08
	copy(eeprom[0x0b:0x1b], []byte{0x30, 0x60, 0x23, 0x43, 0x25, 0x77, 0x8c, 0x41, 0x9d, 0x48, 0xbf, 0xec, 0x0e, 0x87, 0x13, 0x71})
	for i := 0; i < 128; i++ {
		eeprom[0x1b+i] = byte(i)
	}
	eeprom[0x9b] = 0x73
	eeprom[0x9c] = 0x07
	eeprom[0x9d] = 0x05
	eeprom[0x9e] = 0x11
	eeprom[0x9f] = 0x00
	eeprom[0xa0] = 0x01
	eeprom[0xa1] = 0xe8
	eeprom[0xa2] = 0x48
	eeprom[0xa3] = 0x19
	eeprom[0xa4] = 0xcf
	eeprom[0xa5] = 0xd1
	eeprom[0xa6] = 0x90
	eeprom[0xa7] = 0x73

	frame := append([]byte{kissFend, rnodeKISSCommandROMRead}, eeprom...)
	frame = append(frame, kissFend)
	return frame
}

func TestCaptureRnodeEEPROMReadsPythonFrame(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{reads: validRnodeEEPROMFrame()}
	state, err := captureRnodeEEPROM("ttyUSB0", serial, time.Second)
	if err != nil {
		t.Fatalf("captureRnodeEEPROM returned error: %v", err)
	}
	if !state.provisioned || !state.configured {
		t.Fatalf("expected provisioned and configured EEPROM, got %#v", state)
	}
	if len(serial.writes) != 1 {
		t.Fatalf("expected one ROM read write, got %v", len(serial.writes))
	}
	if !bytes.Equal(serial.writes[0], []byte{kissFend, rnodeKISSCommandROMRead, 0x00, kissFend}) {
		t.Fatalf("ROM read command mismatch: %x", serial.writes[0])
	}
}

func TestCaptureRnodeEEPROMTimesOut(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{blockOnEmpty: true, wait: make(chan struct{})}
	_, err := captureRnodeEEPROM("ttyUSB0", serial, 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if err.Error() != "timed out while reading device EEPROM" {
		t.Fatalf("unexpected error: %v", err)
	}
}
