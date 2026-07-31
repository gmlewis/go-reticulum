// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunEEPROMDumpPrintsColonHexContents(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{reads: validRnodeEEPROMFrame()}
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}}

	var out bytes.Buffer
	if err := rt.runEEPROMDump(&out, "ttyUSB0"); err != nil {
		t.Fatalf("runEEPROMDump returned error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "EEPROM contents:") {
		t.Fatalf("missing EEPROM dump label: %q", got)
	}
	if !strings.Contains(got, "03:a4:05:01:02:03:04:05:06:07:08") {
		t.Fatalf("missing EEPROM hex output: %q", got)
	}
	if len(serial.writes) != 1 {
		t.Fatalf("expected one EEPROM read write, got %v", len(serial.writes))
	}
}
