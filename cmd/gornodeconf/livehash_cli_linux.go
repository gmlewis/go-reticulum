// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"time"
)

func runFirmwareHashReadbacks(out io.Writer, port string, opts options) (err error) {
	return newRuntime().runFirmwareHashReadbacks(out, port, opts)
}

func (rt cliRuntime) runFirmwareHashReadbacks(out io.Writer, port string, opts options) (err error) {
	serial, err := rt.rnodeOpenSerial(port)
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
		return errors.New("This device has not been provisioned yet, cannot get firmware hash")
	}

	snapshot, err := captureRnodeHashes(serial, 5*time.Second)
	if err != nil {
		return err
	}

	if opts.getTargetFirmwareHash {
		if _, err := fmt.Fprintf(out, "The target firmware hash is: %x\n", snapshot.firmwareHashTarget); err != nil {
			return err
		}
	}
	if opts.getFirmwareHash {
		if _, err := fmt.Fprintf(out, "The actual firmware hash is: %x\n", snapshot.firmwareHash); err != nil {
			return err
		}
	}
	return nil
}
