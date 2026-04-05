// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func runFirmwareFlash(out io.Writer, port string, opts options) error {
	return newRuntime().runFirmwareFlash(out, port, opts)
}

func (rt cliRuntime) runFirmwareFlash(out io.Writer, port string, opts options) (err error) {
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

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}
	firmwareDir := filepath.Join(configDir, "update", plan.selectedVersion)
	if opts.useExtracted {
		firmwareDir = plan.extractedDir
	}
	if firmwareDir == "" {
		firmwareDir = filepath.Join(configDir, "update", "latest")
	}

	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	platform, err := readRnodePlatform(port, serial, 5*time.Second)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintln(out, "Flashing RNode firmware to device on "+port); err != nil {
		return err
	}
	args, err := flasherCommandCall(platform, 0xa1, port, opts.baudFlash, firmwareDir, plan.firmwareFilename)
	if err != nil {
		return err
	}
	if _, err := rt.runCommand(args[0], args[1:]...); err != nil {
		return fmt.Errorf("the flashing command did not complete successfully: %v", err)
	}
	if _, err := fmt.Fprintln(out, "Done flashing"); err != nil {
		return err
	}
	return nil
}
