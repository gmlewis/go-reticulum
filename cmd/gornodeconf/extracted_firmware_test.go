// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadExtractedFirmwareReleaseInfo(t *testing.T) {
	t.Parallel()

	dir := tempExtractedFirmwareDir(t)
	for _, name := range extractedFirmwareRequiredFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("write %v: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "extracted_rnode_firmware.version"), []byte("1.2.3 deadbeef"), 0o644); err != nil {
		t.Fatalf("write version file: %v", err)
	}

	version, hash, err := readExtractedFirmwareReleaseInfo(dir)
	if err != nil {
		t.Fatalf("readExtractedFirmwareReleaseInfo returned error: %v", err)
	}
	if version != "1.2.3" || hash != "deadbeef" {
		t.Fatalf("unexpected extracted firmware metadata: %q %q", version, hash)
	}
}

func TestReadExtractedFirmwareReleaseInfoMissingVersionFile(t *testing.T) {
	t.Parallel()

	dir := tempExtractedFirmwareDir(t)
	_, _, err := readExtractedFirmwareReleaseInfo(dir)
	if err == nil || !strings.Contains(err.Error(), "no extracted firmware is available") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadExtractedFirmwareReleaseInfoMissingParts(t *testing.T) {
	t.Parallel()

	dir := tempExtractedFirmwareDir(t)
	if err := os.WriteFile(filepath.Join(dir, "extracted_rnode_firmware.version"), []byte("1.2.3 deadbeef"), 0o644); err != nil {
		t.Fatalf("write version file: %v", err)
	}
	_, _, err := readExtractedFirmwareReleaseInfo(dir)
	if err == nil || !strings.Contains(err.Error(), "one or more required firmware files are missing") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func tempExtractedFirmwareDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "gornodeconf-extracted-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
