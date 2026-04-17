// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package main

import (
	"fmt"
	"io"
	"os"
	"time"
)

func runFirmwareUpdate(out io.Writer, port string, opts options) error {
	return newRuntime().runFirmwareUpdate(out, port, opts)
}

func (rt cliRuntime) runFirmwareUpdate(out io.Writer, port string, opts options) (err error) {
	if opts.useExtracted {
		input := rt.stdin
		if input == nil {
			input = os.Stdin
		}
		if err := promptUseExtractedFirmware(out, input); err != nil {
			return err
		}
	}

	plan, err := resolveFirmwareDownloadPlan(opts, "rnode_firmware.zip")
	if err != nil {
		return err
	}
	_ = plan

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
	if !eepromState.provisioned {
		return fmt.Errorf("This device has not been provisioned yet, cannot update firmware")
	}

	state := &firmwareUpdateIndicatorState{name: "rnode", writer: serial}
	if err := state.indicateFirmwareUpdate(); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "Firmware update mode requested"); err != nil {
		return err
	}
	return nil
}
