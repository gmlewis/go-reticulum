// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestCaptureRnodeEEPROMReadsPythonFrame(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)

	serial := &liveHashSerial{reads: validRnodeEEPROMFrame()}
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
	testutils.SkipShortIntegration(t)

	serial := &liveHashSerial{blockOnEmpty: true, wait: make(chan struct{})}
	_, err := captureRnodeEEPROM("ttyUSB0", serial, 5*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if err.Error() != "timed out while reading device EEPROM" {
		t.Fatalf("unexpected error: %v", err)
	}
}
