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

// NewAX25KISSInterface serves as a structurally compliant but functionally inert stub for operating systems lacking rigorous serial port support.
// It deliberately returns an error, ensuring cross-platform compilation succeeds while explicitly declining execution where hardware constraints forbid it.
func NewAX25KISSInterface(name, port string, speed, databits, stopbits int, parity, callsign string, ssid, preambleMS, txTailMS, persistence, slotTimeMS int, flowControl bool, handler InboundHandler) (Interface, error) {
	return nil, fmt.Errorf("serial port not supported on platform %v", runtime.GOOS)
}
