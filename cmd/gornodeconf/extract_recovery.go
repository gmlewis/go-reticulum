// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

const (
	recoveryEsptoolFilename = "recovery_esptool.py"
	recoveryEsptoolSHA256   = "3ff47c999807a1ecf732016f65b6b863c3c80f56e39464694490c4a0c456b943"
)

// recoveryEsptoolSource is generated mechanically from the original Python
// source-of-truth extract_recovery_esptool() helper.
//
//go:embed recovery_esptool.py
var recoveryEsptoolSource []byte

func recoveryEsptoolPath(dir string) string {
	return filepath.Join(dir, recoveryEsptoolFilename)
}

func ensureRecoveryEsptoolAt(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if current, err := os.ReadFile(path); err == nil {
		if bytes.Equal(current, recoveryEsptoolSource) {
			return os.Chmod(path, 0o755)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, recoveryEsptoolSource, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}

func ensureRecoveryEsptoolInDir(dir string) (string, error) {
	path := recoveryEsptoolPath(dir)
	return path, ensureRecoveryEsptoolAt(path)
}

// extractTarget describes one flash region read by the extraction workflow.
type extractTarget struct {
	name     string
	offset   int
	size     int
	filename string
}

// defaultExtractTargets returns the Python source-of-truth extraction targets
// in the order they are read from the device.
func defaultExtractTargets() []extractTarget {
	return []extractTarget{
		{name: "bootloader", offset: 0x1000, size: 0x4650, filename: "extracted_rnode_firmware.bootloader"},
		{name: "partitions", offset: 0x8000, size: 0x0c00, filename: "extracted_rnode_firmware.partitions"},
		{name: "boot_app0", offset: 0xe000, size: 0x2000, filename: "extracted_rnode_firmware.boot_app0"},
		{name: "firmware", offset: 0x10000, size: 0x200000, filename: "extracted_rnode_firmware.bin"},
		{name: "console", offset: 0x210000, size: 0x1f0000, filename: "extracted_console_image.bin"},
	}
}

// recoveryEsptoolCommandArgs returns the exact command-line arguments used to
// read one flash region with the Python recovery esptool helper.
func recoveryEsptoolCommandArgs(esptoolPath, port, baud string, offset, size int, outputFile string) []string {
	return []string{
		esptoolPath,
		"--chip", "esp32",
		"--port", port,
		"--baud", baud,
		"--before", "default_reset",
		"--after", "hard_reset",
		"read_flash",
		fmt.Sprintf("0x%x", offset),
		fmt.Sprintf("0x%x", size),
		outputFile,
	}
}
