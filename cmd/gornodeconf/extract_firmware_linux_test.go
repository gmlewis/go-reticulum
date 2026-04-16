// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestRunFirmwareExtractWritesExpectedArtifacts(t *testing.T) {
	home := tempExtractFirmwareHome(t)
	t.Setenv("HOME", home)

	configDir, err := rnodeconfConfigDir()
	if err != nil {
		t.Fatalf("rnodeconfConfigDir returned error: %v", err)
	}
	extractedDir := filepath.Join(configDir, "extracted")

	serial := &scriptedSerial{reads: append([]byte{kissFend, rnodeKISSCommandPlatform, romPlatformESP32, kissFend}, []byte{
		kissFend, rnodeKISSCommandFWVersion, 0x02, 0x05, kissFend,
		kissFend, rnodeKISSCommandDevHash,
		0x01, 0x02, 0x03, 0x04,
		0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c,
		0x0d, 0x0e, 0x0f, 0x10,
		0x11, 0x12, 0x13, 0x14,
		0x15, 0x16, 0x17, 0x18,
		0x19, 0x1a, 0x1b, 0x1c,
		0x1d, 0x1e, 0x1f, 0x20, kissFend,
		kissFend, rnodeKISSCommandHashes,
		0x01,
		0xa1, 0xa2, 0xa3, 0xa4,
		0xa5, 0xa6, 0xa7, 0xa8,
		0xa9, 0xaa, 0xab, 0xac,
		0xad, 0xae, 0xaf, 0xb0,
		0xb1, 0xb2, 0xb3, 0xb4,
		0xb5, 0xb6, 0xb7, 0xb8,
		0xb9, 0xba, 0xbb, 0xbc,
		0xbd, 0xbe, 0xbf, kissFesc, kissTfend, kissFend,
		kissFend, rnodeKISSCommandHashes,
		0x02,
		0xc1, 0xc2, 0xc3, 0xc4,
		0xc5, 0xc6, 0xc7, 0xc8,
		0xc9, 0xca, 0xcb, 0xcc,
		0xcd, 0xce, 0xcf, 0xd0,
		0xd1, 0xd2, 0xd3, 0xd4,
		0xd5, 0xd6, 0xd7, 0xd8,
		0xd9, 0xda, kissFesc, kissTfesc, 0xdc, 0xdd, 0xde, 0xdf, 0xe0, kissFend,
	}...)}

	writtenCommands := make([][]string, 0, len(defaultExtractTargets()))
	rt := cliRuntime{
		openSerial: func(settings serialSettings) (serialPort, error) {
			if settings.Port != "ttyUSB0" {
				t.Fatalf("unexpected port setting: %+v", settings)
			}
			return serial, nil
		},
		runCommand: func(name string, args ...string) ([]byte, error) {
			writtenCommands = append(writtenCommands, append([]string{name}, args...))
			outputFile := args[len(args)-1]
			if err := os.WriteFile(outputFile, []byte(filepath.Base(outputFile)), 0o644); err != nil {
				return nil, err
			}
			return nil, nil
		},
		stdin: strings.NewReader("\n"),
	}

	var out bytes.Buffer
	if err := rt.runFirmwareExtract(&out, "ttyUSB0", options{baudFlash: "921600"}); err != nil {
		t.Fatalf("runFirmwareExtract returned error: %v\n%v", err, out.String())
	}

	for _, want := range []string{
		"RNode Firmware Extraction",
		"Ready to extract firmware images from the RNode",
		"Press enter to start the extraction process",
		"Firmware successfully extracted!",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing output %q in %q", want, out.String())
		}
	}

	if len(writtenCommands) != len(defaultExtractTargets()) {
		t.Fatalf("command count mismatch: got %v want %v", len(writtenCommands), len(defaultExtractTargets()))
	}
	for i, target := range defaultExtractTargets() {
		want := recoveryEsptoolCommandArgs(filepath.Join(configDir, "recovery_esptool.py"), "ttyUSB0", "921600", target.offset, target.size, filepath.Join(extractedDir, target.filename))
		if !reflect.DeepEqual(writtenCommands[i], want) {
			t.Fatalf("command %v mismatch:\n got: %#v\nwant: %#v", i, writtenCommands[i], want)
		}
		if data, err := os.ReadFile(filepath.Join(extractedDir, target.filename)); err != nil || string(data) != filepath.Base(filepath.Join(extractedDir, target.filename)) {
			t.Fatalf("unexpected artifact %v: data=%q err=%v", target.filename, data, err)
		}
	}

	versionData, err := os.ReadFile(filepath.Join(extractedDir, "extracted_rnode_firmware.version"))
	if err != nil {
		t.Fatalf("read version file: %v", err)
	}
	if got := strings.TrimSpace(string(versionData)); got != hex.EncodeToString([]byte{
		0xc1, 0xc2, 0xc3, 0xc4,
		0xc5, 0xc6, 0xc7, 0xc8,
		0xc9, 0xca, 0xcb, 0xcc,
		0xcd, 0xce, 0xcf, 0xd0,
		0xd1, 0xd2, 0xd3, 0xd4,
		0xd5, 0xd6, 0xd7, 0xd8,
		0xd9, 0xda, 0xdb, 0xdc,
		0xdd, 0xde, 0xdf, 0xe0,
	}) {
		t.Fatalf("version hash mismatch: got %q", got)
	}
}

func TestRunFirmwareExtractRejectsNonESP32(t *testing.T) {
	t.Parallel()

	serial := &scriptedSerial{reads: []byte{kissFend, rnodeKISSCommandPlatform, romPlatformAVR, kissFend}}
	rt := cliRuntime{openSerial: func(settings serialSettings) (serialPort, error) {
		return serial, nil
	}}

	var out bytes.Buffer
	err := rt.runFirmwareExtract(&out, "ttyUSB0", options{baudFlash: "921600"})
	if err == nil || !strings.Contains(err.Error(), "Firmware extraction is currently only supported on ESP32-based RNodes.") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func tempExtractFirmwareHome(t *testing.T) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-extract-firmware-*")
	t.Cleanup(cleanup)
	return dir
}
