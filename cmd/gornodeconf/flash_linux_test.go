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
	"reflect"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestRunFirmwareFlashUsesExtractedFirmwareCommand(t *testing.T) {
	home := tempFlashHome(t)
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

	serial := &scriptedSerial{reads: []byte{kissFend, rnodeKISSCommandPlatform, romPlatformESP32, kissFend}}
	var gotArgs []string
	rt := cliRuntime{
		openSerial: func(settings serialSettings) (serialPort, error) {
			return serial, nil
		},
		runCommand: func(name string, args ...string) ([]byte, error) {
			gotArgs = append([]string{name}, args...)
			return nil, nil
		},
		stdin: strings.NewReader("\n"),
	}

	var out bytes.Buffer
	if err := rt.runFirmwareFlash(&out, "ttyUSB0", options{useExtracted: true, baudFlash: "921600"}); err != nil {
		t.Fatalf("runFirmwareFlash returned error: %v\n%v", err, out.String())
	}
	wantArgs := []string{
		filepath.Join(configDir, "extracted", "recovery_esptool.py"),
		"--chip", "esp32",
		"--port", "ttyUSB0",
		"--baud", "921600",
		"--before", "default_reset",
		"--after", "hard_reset",
		"write_flash", "-z",
		"--flash_mode", "dio",
		"--flash_freq", "80m",
		"--flash_size", "4MB",
		"0xe000", filepath.Join(extractedDir, "extracted_rnode_firmware.zip.boot_app0"),
		"0x1000", filepath.Join(extractedDir, "extracted_rnode_firmware.zip.bootloader"),
		"0x10000", filepath.Join(extractedDir, "extracted_rnode_firmware.zip.bin"),
		"0x210000", filepath.Join(extractedDir, "extracted_console_image.bin"),
		"0x8000", filepath.Join(extractedDir, "extracted_rnode_firmware.zip.partitions"),
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("flash command mismatch:\n got: %#v\nwant: %#v", gotArgs, wantArgs)
	}
	if _, err := os.Stat(filepath.Join(extractedDir, "recovery_esptool.py")); err != nil {
		t.Fatalf("expected recovery helper in extracted dir: %v", err)
	}
	if !strings.Contains(out.String(), "Flashing RNode firmware to device on ttyUSB0") || !strings.Contains(out.String(), "Done flashing") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestRunFirmwareFlashRejectsUnsupportedPlatform(t *testing.T) {
	home := tempFlashHome(t)
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

	serial := &scriptedSerial{reads: []byte{kissFend, rnodeKISSCommandPlatform, romPlatformNRF52, kissFend}}
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}, stdin: strings.NewReader("\n")}

	var out bytes.Buffer
	err = rt.runFirmwareFlash(&out, "ttyUSB0", options{useExtracted: true})
	if err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunFirmwareFlashClosesSerialBeforeRunningFlasher(t *testing.T) {
	home := tempFlashHome(t)
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

	serial := &scriptedSerial{reads: []byte{kissFend, rnodeKISSCommandPlatform, romPlatformESP32, kissFend}}
	rt := cliRuntime{
		openSerial: func(settings serialSettings) (serialPort, error) {
			return serial, nil
		},
		runCommand: func(name string, args ...string) ([]byte, error) {
			if !serial.closed {
				t.Fatal("expected serial port to be closed before flasher command")
			}
			return nil, nil
		},
		stdin: strings.NewReader("\n"),
	}

	var out bytes.Buffer
	if err := rt.runFirmwareFlash(&out, "ttyUSB0", options{useExtracted: true, baudFlash: "921600"}); err != nil {
		t.Fatalf("runFirmwareFlash returned error: %v\n%v", err, out.String())
	}
}

func tempFlashHome(t *testing.T) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-flash-*")
	t.Cleanup(cleanup)
	return dir
}
