// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"
)

func runFirmwareHashSet(out io.Writer, port, hashHex string) (err error) {
	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil || len(hashBytes) != 32 {
		return errors.New("The provided value was not a valid SHA256 hash")
	}

	serial, err := rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	eepromState, err := captureRnodeEEPROM(serial, 5*time.Second)
	if err != nil {
		return err
	}
	if !eepromState.provisioned {
		return errors.New("This device has not been provisioned yet, cannot set firmware hash")
	}

	state := &firmwareHashSetterState{name: "rnode", hashBytes: hashBytes, writer: serial}
	if err := state.setFirmwareHash(); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "Firmware hash set"); err != nil {
		return err
	}
	return nil
}
