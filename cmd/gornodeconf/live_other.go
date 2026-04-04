// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux

package main

import (
	"fmt"
	"io"
	"runtime"
)

func resolveLivePort(port string, opts options) (string, error) {
	if port != "" {
		return port, nil
	}
	if opts.sign || opts.firmwareHash != "" || opts.getTargetFirmwareHash || opts.getFirmwareHash {
		return "", fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
	}
	return "", nil
}

func runFirmwareHashReadbacks(out io.Writer, port string, opts options) error {
	return fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}

func runFirmwareHashSet(out io.Writer, port, hashHex string) error {
	return fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}

func runDeviceSigning(out io.Writer, port string) error {
	return fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
