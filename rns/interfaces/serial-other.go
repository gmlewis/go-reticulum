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
	SerialDefaultSpeed    = 9600
	SerialDefaultDataBits = 8
	SerialDefaultStopBits = 1
	SerialDefaultParity   = "N"
)

// NewSerialInterface provides a safe compilation fallback for environments lacking native serial TTY hardware access.
// It systematically returns an error to gracefully prevent runtime panics when invoked on unsupported architectures.
func NewSerialInterface(name, port string, speed, databits, stopbits int, parity string, handler InboundHandler) (Interface, error) {
	return nil, fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
