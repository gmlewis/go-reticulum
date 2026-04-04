// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func verifyFirmwareHash(contents []byte, expectedHex string) error {
	fileHash := sha256.Sum256(contents)
	if hex.EncodeToString(fileHash[:]) == expectedHex {
		return nil
	}
	return fmt.Errorf("Firmware hash %x but should be %v, possibly due to download corruption.\nFirmware corrupt. Try clearing the local firmware cache with: rnodeconf --clear-cache", fileHash, expectedHex)
}
