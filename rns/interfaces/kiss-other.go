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
	KISSDefaultSpeed    = 9600
	KISSDefaultDataBits = 8
	KISSDefaultStopBits = 1
	KISSDefaultParity   = "N"
)

func NewKISSInterface(name, port string, speed, databits, stopbits int, parity string, handler InboundHandler) (Interface, error) {
	return nil, fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
