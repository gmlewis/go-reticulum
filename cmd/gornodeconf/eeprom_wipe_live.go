// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin || linux

package main

import (
	"fmt"
	"io"
	"time"
)

func runEEPROMWipe(out io.Writer, port string) error {
	return newRuntime().runEEPROMWipe(out, port)
}

func (rt cliRuntime) runEEPROMWipe(out io.Writer, port string) (err error) {
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := serial.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	if _, err := fmt.Fprintln(out, "WARNING: EEPROM is being wiped! Power down device NOW if you do not want this!"); err != nil {
		return err
	}

	platform, err := readRnodePlatform(port, serial, 5*time.Second)
	if err != nil {
		return err
	}

	state := &modeSwitchState{platform: platform, writer: serial, sleeper: rt}
	if err := state.wipeEEPROM(); err != nil {
		return err
	}
	if state.platform != romPlatformNRF52 {
		if err := state.hardReset(); err != nil {
			return err
		}
	}
	return nil
}
