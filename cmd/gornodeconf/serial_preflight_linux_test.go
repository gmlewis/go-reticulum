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
	t.Parallel()
	serial := &stubSerial{}
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}}

	got, err := rt.preflightRnodeSerial("ttyUSB0")
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
	t.Parallel()
	wantErr := errors.New("serial missing")
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return nil, wantErr
	}}

	got, err := rt.preflightRnodeSerial("ttyUSB0")
	if !errors.Is(err, wantErr) {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil port, got %T", got)
	}
}
