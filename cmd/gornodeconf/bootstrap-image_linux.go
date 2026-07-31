// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

func bootstrapEEPROMImage(product, model byte, hwRev byte, serialno, timestamp uint32, signature []byte) []byte {
	image := make([]byte, 0xa8)
	image[0x00] = product
	image[0x01] = model
	image[0x02] = hwRev
	image[0x03] = byte(serialno >> 24)
	image[0x04] = byte(serialno >> 16)
	image[0x05] = byte(serialno >> 8)
	image[0x06] = byte(serialno)
	image[0x07] = byte(timestamp >> 24)
	image[0x08] = byte(timestamp >> 16)
	image[0x09] = byte(timestamp >> 8)
	image[0x0a] = byte(timestamp)
	checksum := checksumInfoHash(product, model, hwRev, serialno, timestamp)
	copy(image[0x0b:0x1b], checksum)
	copy(image[0x1b:0x1b+len(signature)], signature)
	image[0x9b] = 0x73
	return image
}
