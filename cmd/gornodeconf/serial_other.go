// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux

package main

import (
	"fmt"
	"runtime"
)

func defaultOpenSerial(settings serialSettings) (serialPort, error) {
	return nil, fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
