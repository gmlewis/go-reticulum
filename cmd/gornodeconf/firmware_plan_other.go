// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !linux

package main

import "errors"

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
	_ = opts
	_ = firmwareFilename
	return firmwareDownloadPlan{}, errors.New("firmware update not supported on this platform")
}
