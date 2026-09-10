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

// deviceID is not implementable on this platform without unix syscall access;
// reporting false disables same-device deduplication (harmless — devices are
// simply probed once per path).
func deviceID(path string) (uint64, bool) { return 0, false }

// flushSerialInput is not implementable on this platform; serial input is
// never flushed (harmless — stale-packet gating still applies).
func flushSerialInput(fd uintptr) error { return nil }

func errTimeout() error { return errors.New("timeout") }

func nocttyFlag() int { return 0 }

func postOpenSerialPort(file *os.File) error { return nil }

func serialBusyCheck(port string) (bool, string) {
	return false, ""
}

func configureSerialPort(fd uintptr, speed int) error {
	return fmt.Errorf("gornode-diagnostics does not support serial ports on this platform")
}
