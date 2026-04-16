// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestRecoveryEsptoolCommandArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		offset     int
		size       int
		outputFile string
		want       []string
	}{
		{
			name:       "bootloader",
			offset:     0x1000,
			size:       0x4650,
			outputFile: "/tmp/extracted_rnode_firmware.bootloader",
			want: []string{
				"/tmp/recovery_esptool.py",
				"--chip", "esp32",
				"--port", "ttyUSB0",
				"--baud", "921600",
				"--before", "default_reset",
				"--after", "hard_reset",
				"read_flash",
				"0x1000",
				"0x4650",
				"/tmp/extracted_rnode_firmware.bootloader",
			},
		},
		{
			name:       "console image",
			offset:     0x210000,
			size:       0x1f0000,
			outputFile: "/tmp/extracted_console_image.bin",
			want: []string{
				"/tmp/recovery_esptool.py",
				"--chip", "esp32",
				"--port", "ttyUSB0",
				"--baud", "921600",
				"--before", "default_reset",
				"--after", "hard_reset",
				"read_flash",
				"0x210000",
				"0x1f0000",
				"/tmp/extracted_console_image.bin",
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := recoveryEsptoolCommandArgs("/tmp/recovery_esptool.py", "ttyUSB0", "921600", test.offset, test.size, test.outputFile); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("recoveryEsptoolCommandArgs mismatch:\n got: %#v\nwant: %#v", got, test.want)
			}
		})
	}
}

func TestRecoveryEsptoolNoStubCommandArgs(t *testing.T) {
	t.Parallel()

	got := recoveryEsptoolNoStubCommandArgs("/tmp/recovery_esptool.py", "ttyUSB0", "921600", 0xe000, 0x2000, "/tmp/extracted_rnode_firmware.boot_app0")
	want := []string{
		"/tmp/recovery_esptool.py",
		"--chip", "auto",
		"--port", "ttyUSB0",
		"--baud", "921600",
		"--before", "usb_reset",
		"--after", "hard_reset",
		"--no-stub",
		"read_flash",
		"0xe000",
		"0x2000",
		"/tmp/extracted_rnode_firmware.boot_app0",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("recoveryEsptoolNoStubCommandArgs mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestEnsureRecoveryEsptoolAtWritesExecutableSourceOfTruthFile(t *testing.T) {
	t.Parallel()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-recovery-helper-*")
	t.Cleanup(cleanup)
	path := filepath.Join(dir, "recovery_esptool.py")
	if err := ensureRecoveryEsptoolAt(path); err != nil {
		t.Fatalf("ensureRecoveryEsptoolAt returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != recoveryEsptoolSHA256 {
		t.Fatalf("helper sha256 = %q, want %q", got, recoveryEsptoolSHA256)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("file mode = %v, want 0o755", info.Mode().Perm())
	}
}

func TestEnsureRecoveryEsptoolAtRewritesStaleFile(t *testing.T) {
	t.Parallel()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-recovery-helper-*")
	t.Cleanup(cleanup)
	path := filepath.Join(dir, "recovery_esptool.py")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}

	if err := ensureRecoveryEsptoolAt(path); err != nil {
		t.Fatalf("ensureRecoveryEsptoolAt returned error: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != recoveryEsptoolSHA256 {
		t.Fatalf("helper sha256 = %q, want %q", got, recoveryEsptoolSHA256)
	}
}

func TestDefaultExtractTargets(t *testing.T) {
	t.Parallel()

	targets := defaultExtractTargets()
	if len(targets) != 5 {
		t.Fatalf("target count mismatch: got %v want 5", len(targets))
	}

	want := []extractTarget{
		{name: "bootloader", offset: 0x1000, size: 0x4650, filename: "extracted_rnode_firmware.bootloader"},
		{name: "partitions", offset: 0x8000, size: 0x0c00, filename: "extracted_rnode_firmware.partitions"},
		{name: "boot_app0", offset: 0xe000, size: 0x2000, filename: "extracted_rnode_firmware.boot_app0"},
		{name: "firmware", offset: 0x10000, size: 0x200000, filename: "extracted_rnode_firmware.bin"},
		{name: "console", offset: 0x210000, size: 0x1f0000, filename: "extracted_console_image.bin"},
	}

	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("target matrix mismatch:\n got: %#v\nwant: %#v", targets, want)
	}
}
