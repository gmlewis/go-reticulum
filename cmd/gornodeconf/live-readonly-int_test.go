// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && (linux || darwin)

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveAutoDetectPort(t *testing.T) {
	port := requireLiveHardwarePort(t, liveSerialSafetyReadOnly)
	t.Logf("expecting a discoverable live device compatible with %v", port)

	rt := newRuntime()
	discovered, err := rt.resolveLivePort("", options{sign: true})
	if err != nil {
		t.Fatalf("resolveLivePort() error: %v", err)
	}
	if discovered == "" {
		t.Fatal("resolveLivePort() returned an empty port")
	}

	serial, err := preflightRnodeSerial(discovered)
	if err != nil {
		t.Fatalf("preflightRnodeSerial(%q): %v", discovered, err)
	}
	defer func() {
		_ = serial.Close()
	}()
}

func TestLiveCaptureRnodeEEPROM(t *testing.T) {
	port, serial := openLiveReadOnlySerial(t)

	state, err := captureRnodeEEPROM(port, serial, 5*time.Second)
	if err != nil {
		t.Fatalf("captureRnodeEEPROM() error: %v", err)
	}
	if !state.provisioned {
		t.Fatalf("expected provisioned EEPROM state, got %#v", state)
	}
	if len(state.eeprom) == 0 {
		t.Fatalf("expected EEPROM bytes, got %#v", state)
	}
}

func TestLiveCaptureRnodeHashes(t *testing.T) {
	_, serial := openLiveReadOnlySerial(t)

	snapshot, err := captureRnodeHashes(serial, 5*time.Second)
	if err != nil {
		t.Fatalf("captureRnodeHashes() error: %v", err)
	}
	if len(snapshot.firmwareHashTarget) != 32 {
		t.Fatalf("expected 32-byte target firmware hash, got %d", len(snapshot.firmwareHashTarget))
	}
	if len(snapshot.firmwareHash) != 32 {
		t.Fatalf("expected 32-byte actual firmware hash, got %d", len(snapshot.firmwareHash))
	}
}

func TestLiveFirmwareHashReadbacksCLI(t *testing.T) {
	port := requireLiveHardwarePort(t, liveSerialSafetyReadOnly)

	out, err := runGornodeconf("--get-target-firmware-hash", "--get-firmware-hash", port)
	if err != nil {
		t.Fatalf("runGornodeconf hash readbacks failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "The target firmware hash is: ") {
		t.Fatalf("missing target firmware hash output:\n%v", out)
	}
	if !strings.Contains(out, "The actual firmware hash is: ") {
		t.Fatalf("missing actual firmware hash output:\n%v", out)
	}
}

func TestLiveEEPROMBackupCLI(t *testing.T) {
	port := requireLiveHardwarePort(t, liveSerialSafetyReadOnly)
	home := liveHardwareTempDir(t, "gornodeconf-live-eeprom-backup-*")

	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "--eeprom-backup", port)
	if err != nil {
		t.Fatalf("runGornodeconf eeprom backup failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "EEPROM backup written to: ") {
		t.Fatalf("missing backup output:\n%v", out)
	}
	backupDir := filepath.Join(home, ".config", "rnodeconf", "eeprom")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", backupDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 EEPROM backup artifact, got %d", len(entries))
	}
}

func TestLiveEEPROMDumpCLI(t *testing.T) {
	port := requireLiveHardwarePort(t, liveSerialSafetyReadOnly)

	out, err := runGornodeconf("--eeprom-dump", port)
	if err != nil {
		t.Fatalf("runGornodeconf eeprom dump failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "EEPROM contents:") {
		t.Fatalf("missing EEPROM dump label:\n%v", out)
	}
	if !strings.Contains(out, ":") {
		t.Fatalf("missing EEPROM hex payload:\n%v", out)
	}
}

func TestLiveFirmwareExtractCLI(t *testing.T) {
	port := requireLiveHardwarePort(t, liveSerialSafetyReadOnly)
	home := liveHardwareTempDir(t, "gornodeconf-live-extract-*")

	out, err := runGornodeconfWithInputAndEnv("\n", map[string]string{"HOME": home}, "--extract", port)
	if err != nil {
		t.Fatalf("runGornodeconf firmware extract failed: %v\n%v", err, out)
	}
	for _, want := range []string{
		"RNode Firmware Extraction",
		"Ready to extract firmware images from the RNode",
		"Firmware successfully extracted!",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing extract output %q:\n%v", want, out)
		}
	}
	configDir := filepath.Join(home, ".config", "rnodeconf")
	if _, err := os.Stat(filepath.Join(configDir, "recovery_esptool.py")); err != nil {
		t.Fatalf("expected materialized recovery helper: %v", err)
	}
	for _, name := range newExtractedFirmwareState().requiredFiles {
		if _, err := os.Stat(filepath.Join(configDir, "extracted", name)); err != nil {
			t.Fatalf("expected extracted artifact %q: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(configDir, "extracted", "extracted_rnode_firmware.version")); err != nil {
		t.Fatalf("expected extracted version file: %v", err)
	}
}

func openLiveReadOnlySerial(t *testing.T) (string, serialPort) {
	t.Helper()

	port := requireLiveHardwarePort(t, liveSerialSafetyReadOnly)
	serial, err := preflightRnodeSerial(port)
	if err != nil {
		t.Skipf("live RNode serial unavailable before test start: %v", err)
	}
	t.Cleanup(func() {
		_ = serial.Close()
	})
	return port, serial
}

func TestLiveFirmwareHashReadbacksDirect(t *testing.T) {
	port := requireLiveHardwarePort(t, liveSerialSafetyReadOnly)
	rt := newRuntime()

	var out bytes.Buffer
	if err := rt.runFirmwareHashReadbacks(&out, port, options{getTargetFirmwareHash: true, getFirmwareHash: true}); err != nil {
		t.Fatalf("runFirmwareHashReadbacks() error: %v", err)
	}
	if !strings.Contains(out.String(), "The target firmware hash is: ") || !strings.Contains(out.String(), "The actual firmware hash is: ") {
		t.Fatalf("unexpected direct readback output:\n%v", out.String())
	}
}
