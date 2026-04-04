// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"errors"
	"testing"
)

func TestPreflightRnodeSerialReturnsOpenPort(t *testing.T) {
	originalOpenSerial := openSerial
	defer func() { openSerial = originalOpenSerial }()

	serial := &stubSerial{}
	openSerial = func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}

	got, err := preflightRnodeSerial("ttyUSB0")
	if err != nil {
		t.Fatalf("preflightRnodeSerial returned error: %v", err)
	}
	if got == nil {
		t.Fatal("expected open serial port")
	}
	if got != serial {
		t.Fatalf("serial mismatch: got %T want %T", got, serial)
	}
}

func TestPreflightRnodeSerialReturnsOpenError(t *testing.T) {
	originalOpenSerial := openSerial
	defer func() { openSerial = originalOpenSerial }()

	wantErr := errors.New("serial missing")
	openSerial = func(settings serialSettings) (serialPort, error) {
		return nil, wantErr
	}

	got, err := preflightRnodeSerial("ttyUSB0")
	if !errors.Is(err, wantErr) {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil port, got %T", got)
	}
}
