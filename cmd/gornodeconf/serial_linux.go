// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"os"
	"syscall"
)

func init() {
	openSerial = openLinuxSerial
}

func openLinuxSerial(settings serialSettings) (serialPort, error) {
	return os.OpenFile(settings.Port, os.O_RDWR|syscall.O_NOCTTY, 0)
}
