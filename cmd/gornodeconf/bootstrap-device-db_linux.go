// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

func writeDeviceIdentityBackup(configDir string, serialno uint32, eeprom []byte) (string, error) {
	backupDir := filepath.Join(configDir, "firmware", "device_db")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	serialBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(serialBytes, serialno)
	path := filepath.Join(backupDir, hex.EncodeToString(serialBytes))
	if err := os.WriteFile(path, append([]byte(nil), eeprom...), 0o644); err != nil {
		return "", fmt.Errorf("could not backup device EEPROM to disk")
	}
	return path, nil
}
