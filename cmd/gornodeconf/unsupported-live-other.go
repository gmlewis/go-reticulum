// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux && !darwin && !freebsd

package main

import (
	"fmt"
	"io"
	"runtime"
)

// The live RNode EEPROM/firmware/signing workflows require a working serial
// termios layer. On platforms without one (everything other than linux,
// darwin, and freebsd) they return a clear "not supported" error instead of
// failing to link. These stubs complete the "other" bucket so the binary
// builds on every GOOS.

func (rt cliRuntime) runEEPROMBootstrap(out io.Writer, port string, opts options) error {
	return fmt.Errorf("EEPROM bootstrap not supported on platform %v", runtime.GOOS)
}

func (rt cliRuntime) runFirmwareHashReadbacks(out io.Writer, port string, opts options) error {
	return fmt.Errorf("firmware hash readback not supported on platform %v", runtime.GOOS)
}

func (rt cliRuntime) runFirmwareHashSet(out io.Writer, port, hashHex string) error {
	return fmt.Errorf("firmware hash set not supported on platform %v", runtime.GOOS)
}

func (rt cliRuntime) runDeviceSigning(out io.Writer, port string) error {
	return fmt.Errorf("device signing not supported on platform %v", runtime.GOOS)
}

func defaultResolveRecoveryPython() (string, error) {
	return "", fmt.Errorf("recovery python not supported on platform %v", runtime.GOOS)
}
