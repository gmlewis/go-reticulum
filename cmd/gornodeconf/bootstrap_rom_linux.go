// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import "fmt"

func bootstrapEEPROM(writer eepromDownloaderWriter, product, model byte, hwRev byte, serialno, timestamp uint32, signature []byte) error {
	infoBytes := [][]byte{
		{0x00, product},
		{0x01, model},
		{0x02, hwRev},
	}
	for _, field := range infoBytes {
		if err := writeEEPROMByte(writer, field[0], field[1]); err != nil {
			return err
		}
	}

	serialBytes := []byte{
		byte(serialno >> 24),
		byte(serialno >> 16),
		byte(serialno >> 8),
		byte(serialno),
	}
	for offset, value := range serialBytes {
		if err := writeEEPROMByte(writer, byte(0x03+offset), value); err != nil {
			return err
		}
	}

	timeBytes := []byte{
		byte(timestamp >> 24),
		byte(timestamp >> 16),
		byte(timestamp >> 8),
		byte(timestamp),
	}
	for offset, value := range timeBytes {
		if err := writeEEPROMByte(writer, byte(0x07+offset), value); err != nil {
			return err
		}
	}

	checksum := checksumInfoHash(product, model, hwRev, serialno, timestamp)
	for offset, value := range checksum {
		if err := writeEEPROMByte(writer, byte(0x0b+offset), value); err != nil {
			return err
		}
	}

	if err := storeSignature(writer, signature); err != nil {
		return err
	}

	if err := writeEEPROMByte(writer, 0x9b, 0x73); err != nil {
		return err
	}

	return nil
}

func formatBootstrapEEPROMError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w", err)
}
