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
	WeaveDefaultSpeed    = 3000000
	WeaveDefaultDataBits = 8
	WeaveDefaultStopBits = 1
	WeaveDefaultParity   = "N"
)

// NewWeaveInterface gracefully stub-fails on host systems lacking the requisite high-speed serial capabilities necessary for Weave hardware.
// It allows the broader routing stack to compile smoothly across disparate platforms without falsely promising unavailable features.
func NewWeaveInterface(name, port string, configuredBitrate int, handler InboundHandler) (Interface, error) {
	return nil, fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
