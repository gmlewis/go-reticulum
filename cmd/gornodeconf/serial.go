// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "time"

const rnodeBaudRate = 115200

type serialPort interface {
	Close() error
}

type serialSettings struct {
	Port             string
	BaudRate         int
	ByteSize         int
	Parity           string
	StopBits         int
	XonXoff          bool
	RTSCTS           bool
	Timeout          time.Duration
	InterByteTimeout *time.Duration
	WriteTimeout     *time.Duration
	DSRDTR           bool
}

type serialOpener func(serialSettings) (serialPort, error)

var openSerial serialOpener

func rnodeOpenSerial(port string) (serialPort, error) {
	return openSerial(serialSettings{
		Port:             port,
		BaudRate:         rnodeBaudRate,
		ByteSize:         8,
		Parity:           "N",
		StopBits:         1,
		XonXoff:          false,
		RTSCTS:           false,
		Timeout:          0,
		DSRDTR:           false,
		InterByteTimeout: nil,
		WriteTimeout:     nil,
	})
}
