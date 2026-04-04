// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCachedFirmwareReleaseInfo(t *testing.T) {
	t.Parallel()

	dir := tempCachedFirmwareDir(t)
	firmwareName := "rnode_firmware.zip"
	versionPath := filepath.Join(dir, firmwareName+".version")
	firmwarePath := filepath.Join(dir, firmwareName)
	contents := []byte("cached firmware")
	hashBytes := sha256.Sum256(contents)
	hash := hex.EncodeToString(hashBytes[:])
	if err := os.WriteFile(versionPath, []byte("4.5.6 "+hash), 0o644); err != nil {
		t.Fatalf("write version file: %v", err)
	}
	if err := os.WriteFile(firmwarePath, contents, 0o644); err != nil {
		t.Fatalf("write firmware file: %v", err)
	}

	version, gotHash, err := loadCachedFirmwareReleaseInfo(dir, firmwareName)
	if err != nil {
		t.Fatalf("loadCachedFirmwareReleaseInfo returned error: %v", err)
	}
	if version != "4.5.6" || gotHash != hash {
		t.Fatalf("unexpected cached firmware metadata: %q %q", version, gotHash)
	}
}

func TestLoadCachedFirmwareReleaseInfoHashMismatch(t *testing.T) {
	t.Parallel()

	dir := tempCachedFirmwareDir(t)
	firmwareName := "rnode_firmware.zip"
	versionPath := filepath.Join(dir, firmwareName+".version")
	firmwarePath := filepath.Join(dir, firmwareName)
	contents := []byte("cached firmware")
	if err := os.WriteFile(versionPath, []byte("4.5.6 deadbeef"), 0o644); err != nil {
		t.Fatalf("write version file: %v", err)
	}
	if err := os.WriteFile(firmwarePath, contents, 0o644); err != nil {
		t.Fatalf("write firmware file: %v", err)
	}

	_, _, err := loadCachedFirmwareReleaseInfo(dir, firmwareName)
	if err == nil || !strings.Contains(err.Error(), "Firmware hash ") || !strings.Contains(err.Error(), "Firmware corrupt. Try clearing the local firmware cache with: rnodeconf --clear-cache") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCachedFirmwareReleaseInfoMissingReleaseInfo(t *testing.T) {
	t.Parallel()

	dir := tempCachedFirmwareDir(t)
	_, _, err := loadCachedFirmwareReleaseInfo(dir, "rnode_firmware.zip")
	if err == nil || !strings.Contains(err.Error(), "could not read locally cached release information") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadCachedFirmwareReleaseInfoMissingFirmwareFile(t *testing.T) {
	t.Parallel()

	dir := tempCachedFirmwareDir(t)
	firmwareName := "rnode_firmware.zip"
	versionPath := filepath.Join(dir, firmwareName+".version")
	if err := os.WriteFile(versionPath, []byte("4.5.6 deadbeef"), 0o644); err != nil {
		t.Fatalf("write version file: %v", err)
	}

	_, _, err := loadCachedFirmwareReleaseInfo(dir, firmwareName)
	if err == nil {
		t.Fatal("expected loadCachedFirmwareReleaseInfo to fail")
	}
	if !strings.Contains(err.Error(), "no such file") && !strings.Contains(err.Error(), "cannot find") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func tempCachedFirmwareDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "gornodeconf-cached-firmware-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
