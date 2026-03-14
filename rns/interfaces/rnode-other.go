// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux

package interfaces

import (
	"fmt"
	"runtime"
)

const (
	RNodeDefaultSpeed    = 115200
	RNodeDefaultDataBits = 8
	RNodeDefaultStopBits = 1
	RNodeDefaultParity   = "N"
)

// NewRNodeInterface is a structurally compliant stub for operating systems without
// rigorous serial port support. It returns an error so cross-platform compilation
// succeeds while explicitly declining execution where hardware constraints
// prevent operation.
func NewRNodeInterface(name, port string, speed, databits, stopbits int, parity string, frequency, bandwidth, txpower, spreadingFactor, codingRate int, flowControl bool, idInterval int, idCallsign string, handler InboundHandler) (Interface, error) {
	return nil, fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
