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

func loadCachedFirmwareReleaseInfo(versionDir, fwFilename string) (string, string, error) {
	versionPath := filepath.Join(versionDir, fwFilename+".version")
	version, hash, err := readFirmwareReleaseInfoFile(versionPath)
	if err != nil {
		return "", "", fmt.Errorf("could not read locally cached release information: %w", err)
	}
	firmwarePath := filepath.Join(versionDir, fwFilename)
	contents, err := os.ReadFile(firmwarePath)
	if err != nil {
		return "", "", err
	}
	if err := verifyFirmwareHash(contents, hash); err != nil {
		return "", "", err
	}
	return version, hash, nil
}
