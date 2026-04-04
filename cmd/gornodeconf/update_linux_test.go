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

func TestRunFirmwareUpdateWritesFirmwareUpdateCommand(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{reads: validRnodeEEPROMFrame()}
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}}

	var out bytes.Buffer
	if err := rt.runFirmwareUpdate(&out, "ttyUSB0", options{fwVersion: "1.2.3"}); err != nil {
		t.Fatalf("runFirmwareUpdate returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Firmware update mode requested") {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if len(serial.writes) != 2 {
		t.Fatalf("expected EEPROM read and update writes, got %v writes", len(serial.writes))
	}
	if got := serial.writes[1]; !bytes.Equal(got, []byte{kissFend, 0x61, 0x01, kissFend}) {
		t.Fatalf("firmware update command mismatch: %x", got)
	}
}

func TestRunFirmwareUpdateRejectsNoCheckWithoutVersion(t *testing.T) {
	t.Parallel()

	_, err := resolveFirmwareDownloadPlan(options{noCheck: true}, "rnode_firmware.zip")
	if err == nil || !strings.Contains(err.Error(), "Online firmware version check was disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}
