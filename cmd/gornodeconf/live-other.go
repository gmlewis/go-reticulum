// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux && !darwin

package main

import (
	"fmt"
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
