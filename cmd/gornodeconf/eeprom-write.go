// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import "fmt"

const rnodeKISSCommandROMWrite = 0x52

// writeEEPROMByte writes one byte to the device EEPROM using the Python KISS
// frame format.
func writeEEPROMByte(writer eepromDownloaderWriter, addr, value byte) error {
	command := []byte{kissFend, rnodeKISSCommandROMWrite, addr, value, kissFend}
	written, err := writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return fmt.Errorf("An IO error occurred while writing EEPROM")
	}
	return nil
}
