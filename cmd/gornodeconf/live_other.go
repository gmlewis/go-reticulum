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

func (rt cliRuntime) resolveLivePort(port string, opts options) (string, error) {
	if port != "" {
		return port, nil
	}
	if opts.sign || opts.firmwareHash != "" || opts.getTargetFirmwareHash || opts.getFirmwareHash {
		return "", fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
	}
	return "", nil
}

func (rt cliRuntime) runFirmwareHashReadbacks(out io.Writer, port string, opts options) error {
	return fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}

func (rt cliRuntime) runFirmwareHashSet(out io.Writer, port, hashHex string) error {
	return fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}

func (rt cliRuntime) runDeviceSigning(out io.Writer, port string) error {
	return fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}

func runFirmwareHashReadbacks(out io.Writer, port string, opts options) error {
	return newRuntime().runFirmwareHashReadbacks(out, port, opts)
}

func runFirmwareHashSet(out io.Writer, port, hashHex string) error {
	return newRuntime().runFirmwareHashSet(out, port, hashHex)
}

func runDeviceSigning(out io.Writer, port string) error {
	return newRuntime().runDeviceSigning(out, port)
}
