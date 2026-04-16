// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func runEEPROMBackup(out io.Writer, port string) error {
	return newRuntime().runEEPROMBackup(out, port)
}

func (rt cliRuntime) runEEPROMBackup(out io.Writer, port string) error {
	serial, err := rt.rnodeOpenSerial(port)
	if err != nil {
		return err
	}
	defer func() {
		_ = serial.Close()
	}()

	eepromState, err := captureRnodeEEPROM(port, serial, 5*time.Second)
	if err != nil {
		return err
	}

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		return err
	}
	now := rt.now
	if now == nil {
		now = time.Now
	}
	path, err := writeEEPROMBackup(configDir, now(), eepromState.eeprom)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out, "EEPROM backup written to: "+path); err != nil {
		return err
	}
	return nil
}

func writeEEPROMBackup(configDir string, timestamp time.Time, eeprom []byte) (string, error) {
	backupDir := filepath.Join(configDir, "eeprom")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	filename := timestamp.Format("2006-01-02_15-04-05") + ".eeprom"
	path := filepath.Join(backupDir, filename)
	if err := os.WriteFile(path, append([]byte(nil), eeprom...), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
