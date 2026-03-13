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

func NewRNodeMultiInterface(name, port string, speed, databits, stopbits int, parity string, idInterval int, idCallsign string, subinterfaces []RNodeMultiSubinterfaceConfig, handler InboundHandler) (Interface, error) {
	return nil, fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
