// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux

package main

import (
	"os"
	"testing"
)

func TestLiveRnodeSerialPreflight(t *testing.T) {
	port := os.Getenv("GORNODECONF_LIVE_SERIAL_PORT")
	if port == "" {
		t.Skip("GORNODECONF_LIVE_SERIAL_PORT not set")
	}

	serial, err := preflightRnodeSerial(port)
	if err != nil {
		t.Skipf("live RNode serial unavailable before test start: %v", err)
	}
	defer func() {
		_ = serial.Close()
	}()
}
