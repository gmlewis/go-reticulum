// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux && !darwin

package main

import (
	"runtime"
	"testing"
)

func TestRnodeOpenSerialReturnsPlatformError(t *testing.T) {
	t.Parallel()
	_, err := rnodeOpenSerial(tempSerialPort(t))
	if err == nil {
		t.Fatalf("expected error on %v", runtime.GOOS)
	}
	want := "serial port not supported on platform " + runtime.GOOS
	if err.Error() != want {
		t.Fatalf("error mismatch: got %q want %q", err.Error(), want)
	}
}
