// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

var extractedFirmwareRequiredFiles = []string{
	"extracted_console_image.bin",
	"extracted_rnode_firmware.bin",
	"extracted_rnode_firmware.boot_app0",
	"extracted_rnode_firmware.bootloader",
	"extracted_rnode_firmware.partitions",
}

func readExtractedFirmwareReleaseInfo(extractedDir string) (string, string, error) {
	vfpath := filepath.Join(extractedDir, "extracted_rnode_firmware.version")
	if _, err := os.Stat(vfpath); err != nil {
		return "", "", fmt.Errorf("no extracted firmware is available")
	}

	for _, requiredFile := range extractedFirmwareRequiredFiles {
		if _, err := os.Stat(filepath.Join(extractedDir, requiredFile)); err != nil {
			return "", "", fmt.Errorf("one or more required firmware files are missing from the extracted RNode firmware archive")
		}
	}

	data, err := os.ReadFile(vfpath)
	if err != nil {
		return "", "", err
	}
	version, hash, err := parseFirmwareReleaseInfo(data)
	if err != nil {
		return "", "", fmt.Errorf("extracted firmware version file is malformed")
	}
	return version, hash, nil
}
