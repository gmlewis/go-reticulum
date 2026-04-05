// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunFirmwareUpdateWritesFirmwareUpdateCommand(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{reads: validRnodeEEPROMFrame()}
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}, stdin: strings.NewReader("\n")}

	var out bytes.Buffer
	if err := rt.runFirmwareUpdate(&out, "ttyUSB0", options{fwVersion: "1.2.3"}); err != nil {
		t.Fatalf("runFirmwareUpdate returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Firmware update mode requested") {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if len(serial.writes) != 2 {
		t.Fatalf("expected EEPROM read and update writes, got %v writes", len(serial.writes))
	}
	if got := serial.writes[1]; !bytes.Equal(got, []byte{kissFend, 0x61, 0x01, kissFend}) {
		t.Fatalf("firmware update command mismatch: %x", got)
	}
}

func TestRunFirmwareUpdateRejectsNoCheckWithoutVersion(t *testing.T) {
	t.Parallel()

	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		t.Fatal("openSerial should not be called when firmware version checks are disabled without a version")
		return nil, nil
	}}

	var out bytes.Buffer
	err := rt.runFirmwareUpdate(&out, "ttyUSB0", options{noCheck: true})
	if err == nil || !strings.Contains(err.Error(), "Online firmware version check was disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunFirmwareUpdateUsesExtractedFirmware(t *testing.T) {
	home := tempUpdateHome(t)
	t.Setenv("HOME", home)

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		t.Fatalf("rnodeconfConfigDir returned error: %v", err)
	}
	extractedDir := filepath.Join(configDir, "extracted")
	if err := os.MkdirAll(extractedDir, 0o755); err != nil {
		t.Fatalf("mkdir extracted dir: %v", err)
	}
	for _, name := range newExtractedFirmwareState().requiredFiles {
		if err := os.WriteFile(filepath.Join(extractedDir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write required file %v: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(extractedDir, "extracted_rnode_firmware.version"), []byte("9.9.9 cafebabe"), 0o644); err != nil {
		t.Fatalf("write extracted version file: %v", err)
	}

	serial := &scriptedSerial{reads: validRnodeEEPROMFrame()}
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}, stdin: strings.NewReader("\n")}

	var out bytes.Buffer
	if err := rt.runFirmwareUpdate(&out, "ttyUSB0", options{useExtracted: true}); err != nil {
		t.Fatalf("runFirmwareUpdate returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Firmware update mode requested") {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if len(serial.writes) != 2 {
		t.Fatalf("expected EEPROM read and update writes, got %v writes", len(serial.writes))
	}
}

func tempUpdateHome(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "gornodeconf-update-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
