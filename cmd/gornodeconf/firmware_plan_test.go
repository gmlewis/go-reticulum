// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestResolveFirmwareDownloadPlanUsesExplicitVersion(t *testing.T) {
	t.Parallel()

	plan, err := resolveFirmwareDownloadPlan(options{fwVersion: "1.2.3"}, "rnode_firmware.zip")
	if err != nil {
		t.Fatalf("resolveFirmwareDownloadPlan returned error: %v", err)
	}
	if plan.selectedVersion != "1.2.3" {
		t.Fatalf("unexpected selected version: %q", plan.selectedVersion)
	}
	if plan.releaseInfoURL != firmwareVersionURL {
		t.Fatalf("unexpected release info url: %q", plan.releaseInfoURL)
	}
	if plan.fallbackURL != fallbackFirmwareVersionURL {
		t.Fatalf("unexpected fallback url: %q", plan.fallbackURL)
	}
	if plan.updateURL != firmwareUpdateURL+"1.2.3/rnode_firmware.zip" {
		t.Fatalf("unexpected update url: %q", plan.updateURL)
	}
}

func TestResolveFirmwareDownloadPlanRejectsNoCheckWithoutVersion(t *testing.T) {
	t.Parallel()

	_, err := resolveFirmwareDownloadPlan(options{noCheck: true}, "rnode_firmware.zip")
	if err == nil || !strings.Contains(err.Error(), "Online firmware version check was disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveFirmwareDownloadPlanUsesExtractedFirmware(t *testing.T) {
	home := tempFirmwarePlanHome(t)
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
		t.Fatalf("write version file: %v", err)
	}

	plan, err := resolveFirmwareDownloadPlan(options{useExtracted: true}, "rnode_firmware.zip")
	if err != nil {
		t.Fatalf("resolveFirmwareDownloadPlan returned error: %v", err)
	}
	if plan.firmwareFilename != "extracted_rnode_firmware.zip" {
		t.Fatalf("unexpected firmware filename: %q", plan.firmwareFilename)
	}
	if plan.selectedVersion != "9.9.9" || plan.selectedHash != "cafebabe" {
		t.Fatalf("unexpected extracted plan: %+v", plan)
	}
}

func tempFirmwarePlanHome(t *testing.T) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-firmware-plan-*")
	t.Cleanup(cleanup)
	return dir
}
