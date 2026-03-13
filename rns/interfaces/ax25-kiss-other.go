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
	AX25KISSDefaultSpeed       = 9600
	AX25KISSDefaultDataBits    = 8
	AX25KISSDefaultStopBits    = 1
	AX25KISSDefaultParity      = "N"
	AX25KISSDefaultPreambleMS  = 350
	AX25KISSDefaultTxTailMS    = 20
	AX25KISSDefaultPersistence = 64
	AX25KISSDefaultSlotTimeMS  = 20
)

func NewAX25KISSInterface(name, port string, speed, databits, stopbits int, parity, callsign string, ssid, preambleMS, txTailMS, persistence, slotTimeMS int, flowControl bool, handler InboundHandler) (Interface, error) {
	return nil, fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
