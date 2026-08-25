// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file in the root directory.

//go:build linux || darwin

package interfaces

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"syscall"
)

// openSerialPort opens the serial device at path and returns the opened file
// plus the port path that actually succeeded.
//
// On macOS the callin device /dev/tty.* is frequently unusable for USB CDC ACM
// radios: open() returns EBUSY even when nothing holds the port, because the
// callin device waits on the callout/modem-control lock. The callout device
// /dev/cu.* is the correct one (pyserial/macOS users conventionally configure
// cu.* for the same reason). So on darwin, when /dev/tty.* open fails with
// EBUSY, this retries the paired /dev/cu.* and returns that path as the
// effective port so callers can remember it for reconnect. This makes a tty.*
// configuration "just work" on macOS across every serial-backed interface
// (Serial, KISS, AX25KISS, Weave, RNode).
func openSerialPort(path string) (*os.File, string, error) {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err == nil {
		return f, path, nil
	}
	if runtime.GOOS == "darwin" && errors.Is(err, syscall.EBUSY) && strings.HasPrefix(path, "/dev/tty.") {
		cu := "/dev/cu." + path[len("/dev/tty."):]
		if cf, e := os.OpenFile(cu, os.O_RDWR|syscall.O_NOCTTY, 0); e == nil {
			return cf, cu, nil
		}
	}
	return nil, path, err
}
