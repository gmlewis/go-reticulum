// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunFirmwareHashSetWritesPythonFrame(t *testing.T) {
	serial := &liveHashSerial{}
	originalOpenSerial := openSerial
	defer func() { openSerial = originalOpenSerial }()
	openSerial = func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}

	var out bytes.Buffer
	if err := runFirmwareHashSet(&out, "ttyUSB0", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"); err != nil {
		t.Fatalf("runFirmwareHashSet returned error: %v", err)
	}

	if !strings.Contains(out.String(), "Firmware hash set") {
		t.Fatalf("unexpected output: %v", out.String())
	}
	if len(serial.writes) != 1 {
		t.Fatalf("expected 1 write, got %v", len(serial.writes))
	}
	want := []byte{0xc0, 0x58,
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		0xc0}
	if !bytes.Equal(serial.writes[0], want) {
		t.Fatalf("firmware hash frame mismatch:\n got: %x\nwant: %x", serial.writes[0], want)
	}
}

func TestRunFirmwareHashSetRejectsInvalidInput(t *testing.T) {
	var out bytes.Buffer
	if err := runFirmwareHashSet(&out, "ttyUSB0", "not-a-hash"); err == nil {
		t.Fatal("expected invalid hash error")
	}
	if err := runFirmwareHashSet(&out, "ttyUSB0", "0011"); err == nil {
		t.Fatal("expected short hash error")
	}
}

func TestRunFirmwareHashSetCanRestoreOriginalValue(t *testing.T) {
	serial := &liveHashSerial{}
	originalOpenSerial := openSerial
	defer func() { openSerial = originalOpenSerial }()
	openSerial = func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}

	var out bytes.Buffer
	original := "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	updated := "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"
	if err := runFirmwareHashSet(&out, "ttyUSB0", updated); err != nil {
		t.Fatalf("runFirmwareHashSet update failed: %v", err)
	}
	if err := runFirmwareHashSet(&out, "ttyUSB0", original); err != nil {
		t.Fatalf("runFirmwareHashSet restore failed: %v", err)
	}
	if len(serial.writes) != 2 {
		t.Fatalf("expected 2 writes, got %v", len(serial.writes))
	}
}
