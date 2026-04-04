// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"io"
	"time"
)

func runFirmwareHashReadbacks(out io.Writer, port string, opts options) error {
	serial, err := rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer serial.Close()

	snapshot, err := captureRnodeHashes(serial, 5*time.Second)
	if err != nil {
		return err
	}

	if opts.getTargetFirmwareHash {
		fmt.Fprintf(out, "The target firmware hash is: %x\n", snapshot.firmwareHashTarget)
	}
	if opts.getFirmwareHash {
		fmt.Fprintf(out, "The actual firmware hash is: %x\n", snapshot.firmwareHash)
	}
	return nil
}
