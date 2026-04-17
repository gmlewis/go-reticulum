// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestDarwinFlashSupportContract(t *testing.T) {
	home := tempDarwinFlashHome(t)
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
		openSerial: func(settings serialSettings) (serialPort, error) { return serial, nil },
		runCommand: func(name string, args ...string) ([]byte, error) { return nil, nil },
		stdin:      strings.NewReader("\n"),
	}

	var out bytes.Buffer
	if err := rt.runFirmwareFlash(&out, "ttyUSB0", options{useExtracted: true, baudFlash: "921600"}); err != nil {
		t.Fatalf("runFirmwareFlash on darwin returned error: %v\n%v", err, out.String())
	}
}

func tempDarwinFlashHome(t *testing.T) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-flash-darwin-*")
	t.Cleanup(cleanup)
	return dir
}

func TestDarwinUpdateSupportContract(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{reads: validRnodeEEPROMFrame()}
	rt := cliRuntime{
		openSerial: func(settings serialSettings) (serialPort, error) { return serial, nil },
		stdin:      strings.NewReader("\n"),
	}

	var out bytes.Buffer
	if err := rt.runFirmwareUpdate(&out, "ttyUSB0", options{fwVersion: "1.2.3"}); err != nil {
		t.Fatalf("runFirmwareUpdate on darwin returned error: %v\n%v", err, out.String())
	}
}
