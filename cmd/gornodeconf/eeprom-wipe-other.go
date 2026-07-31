// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux && !darwin

package main

import (
	"fmt"
	"io"
	"runtime"
)

func runEEPROMWipe(out io.Writer, port string) error {
	_ = out
	_ = port
	return fmt.Errorf("eeprom wipe not supported on platform %v", runtime.GOOS)
}

func (rt cliRuntime) runEEPROMWipe(out io.Writer, port string) error {
	_ = rt
	return runEEPROMWipe(out, port)
}
