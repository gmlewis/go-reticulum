// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"path/filepath"
)

type firmwareDownloadPlan struct {
	firmwareFilename string
	selectedVersion  string
	selectedHash     string
	releaseInfoURL   string
	fallbackURL      string
	updateURL        string
	extractedDir     string
}

func resolveFirmwareDownloadPlan(opts options, firmwareFilename string) (firmwareDownloadPlan, error) {
	plan := firmwareDownloadPlan{firmwareFilename: firmwareFilename}

	if opts.useExtracted {
		configDir, err := rnodeconfConfigDir()
		if err != nil {
			return plan, err
		}
		plan.firmwareFilename = "extracted_rnode_firmware.zip"
		plan.extractedDir = filepath.Join(configDir, "extracted")
		version, hash, err := readExtractedFirmwareReleaseInfo(plan.extractedDir)
		if err != nil {
			return plan, err
		}
		plan.selectedVersion = version
		plan.selectedHash = hash
		return plan, nil
	}

	if opts.fwVersion != "" {
		plan.selectedVersion = opts.fwVersion
	}

	if opts.noCheck && plan.selectedVersion == "" {
		return plan, errors.New("Online firmware version check was disabled, but no firmware version specified for install.\nuse the --fw-version option to manually specify a version.")
	}

	plan.releaseInfoURL = firmwareReleaseInfoURL(opts.fwURL, plan.selectedVersion)
	plan.fallbackURL = fallbackFirmwareVersionURL
	plan.updateURL = firmwareBinaryURL(opts.fwURL, plan.selectedVersion, plan.firmwareFilename)
	return plan, nil
}

func (p firmwareDownloadPlan) describe() string {
	if p.selectedVersion == "" {
		return fmt.Sprintf("%v (latest)", p.firmwareFilename)
	}
	return fmt.Sprintf("%v @ %v", p.firmwareFilename, p.selectedVersion)
}
