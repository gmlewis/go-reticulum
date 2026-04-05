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

func runEEPROMDump(out io.Writer, port string) error {
	return newRuntime().runEEPROMDump(out, port)
}

func (rt cliRuntime) runEEPROMDump(out io.Writer, port string) (err error) {
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	eepromState, err := captureRnodeEEPROM(port, serial, 5*time.Second)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "EEPROM contents:"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, colonHex(eepromState.eeprom)); err != nil {
		return err
	}
	return nil
}
