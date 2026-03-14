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

// NewRNodeMultiInterface acts as a non-functional compilation stub for unsupported platforms.
// It reliably returns an error, failing safely when multiplexed RNode support is physically impossible on the host OS.
func NewRNodeMultiInterface(name, port string, speed, databits, stopbits int, parity string, idInterval int, idCallsign string, subinterfaces []RNodeMultiSubinterfaceConfig, handler InboundHandler) (Interface, error) {
	return nil, fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
