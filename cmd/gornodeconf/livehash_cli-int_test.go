// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux
// +build integration,linux

package main

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestRunFirmwareHashReadbacksPrintsPythonLinesCLI(t *testing.T) {
	t.Parallel()
	serial := &liveHashSerial{reads: append(validRnodeEEPROMFrame(), []byte{
		kissFend, rnodeKISSCommandFWVersion, 0x02, 0x05, kissFend,
		kissFend, rnodeKISSCommandDevHash,
		0x01, 0x02, 0x03, 0x04,
		0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c,
		0x0d, 0x0e, 0x0f, 0x10,
		0x11, 0x12, 0x13, 0x14,
		0x15, 0x16, 0x17, 0x18,
		0x19, 0x1a, 0x1b, 0x1c,
		0x1d, 0x1e, 0x1f, 0x20, kissFend,
		kissFend, rnodeKISSCommandHashes,
		0x01,
		0xa1, 0xa2, 0xa3, 0xa4,
		0xa5, 0xa6, 0xa7, 0xa8,
		0xa9, 0xaa, 0xab, 0xac,
		0xad, 0xae, 0xaf, 0xb0,
		0xb1, 0xb2, 0xb3, 0xb4,
		0xb5, 0xb6, 0xb7, 0xb8,
		0xb9, 0xba, 0xbb, 0xbc,
		0xbd, 0xbe, 0xbf, kissFesc, kissTfend, kissFend,
		kissFend, rnodeKISSCommandHashes,
		0x02,
		0xc1, 0xc2, 0xc3, 0xc4,
		0xc5, 0xc6, 0xc7, 0xc8,
		0xc9, 0xca, 0xcb, 0xcc,
		0xcd, 0xce, 0xcf, 0xd0,
		0xd1, 0xd2, 0xd3, 0xd4,
		0xd5, 0xd6, 0xd7, 0xd8,
		0xd9, 0xda, kissFesc, kissTfesc, 0xdc, 0xdd, 0xde, 0xdf, 0xe0, kissFend,
	}...)}

	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}}

	var out bytes.Buffer
	if err := rt.runFirmwareHashReadbacks(&out, "ttyUSB0", options{getTargetFirmwareHash: true, getFirmwareHash: true}); err != nil {
		t.Fatalf("runFirmwareHashReadbacks returned error: %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("expected firmware hash output")
	}
}

func TestRunFirmwareHashReadbacksReturnsTimeoutErrorCLI(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)
	serial := &liveHashSerial{blockOnEmpty: true, wait: make(chan struct{})}
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}}

	var out bytes.Buffer
	err := rt.runFirmwareHashReadbacks(&out, "ttyUSB0", options{getTargetFirmwareHash: true})
	if err == nil || err.Error() != "timed out while reading device EEPROM" {
		t.Fatalf("unexpected error: %v", err)
	}
}
