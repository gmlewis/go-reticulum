// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && (linux || darwin)

package main

import (
	"testing"
)

func TestLiveRnodeSerialPreflight(t *testing.T) {
	t.Parallel()
	port := requireLiveHardwarePort(t, liveSerialSafetyReadOnly)

	serial, err := preflightRnodeSerial(port)
	if err != nil {
		t.Skipf("live RNode serial unavailable before test start: %v", err)
	}
	defer func() {
		_ = serial.Close()
	}()
}
