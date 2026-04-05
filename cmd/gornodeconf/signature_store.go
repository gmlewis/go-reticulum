// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import "fmt"

const rnodeKISSCommandDeviceSignature = 0x57

// storeSignature writes a device signature using the Python KISS frame format.
func storeSignature(writer eepromDownloaderWriter, sigBytes []byte) error {
	payload := append([]byte{rnodeKISSCommandDeviceSignature}, kissEscape(sigBytes)...)
	command := append([]byte{kissFend}, payload...)
	command = append(command, kissFend)
	written, err := writer.Write(command)
	if err != nil {
		return err
	}
	if written != len(command) {
		return fmt.Errorf("An IO error occurred while storing signature")
	}
	return nil
}
