// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux && !darwin

package main

import (
	"errors"
	"fmt"
	"os"
)

// Platform stubs: gornode-diagnostics needs raw termios control of a serial
// device, which this build does not support (the RNode fleet tooling targets
// Linux and macOS).

func errBusy() error { return errors.New("busy") }

func errTimeout() error { return errors.New("timeout") }

func nocttyFlag() int { return 0 }

func postOpenSerialPort(file *os.File) error { return nil }

func serialBusyCheck(port string) (bool, string) {
	return false, ""
}

func configureSerialPort(fd uintptr, speed int) error {
	return fmt.Errorf("gornode-diagnostics does not support serial ports on this platform")
}
