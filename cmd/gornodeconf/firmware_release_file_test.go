// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFirmwareReleaseInfoFile(t *testing.T) {
	t.Parallel()

	dir := tempFirmwareReleaseFileDir(t)
	path := filepath.Join(dir, "release.version")
	if err := os.WriteFile(path, []byte("3.0.0 abcdef"), 0o644); err != nil {
		t.Fatalf("write release file: %v", err)
	}

	version, hash, err := readFirmwareReleaseInfoFile(path)
	if err != nil {
		t.Fatalf("readFirmwareReleaseInfoFile returned error: %v", err)
	}
	if version != "3.0.0" || hash != "abcdef" {
		t.Fatalf("unexpected release file contents: %q %q", version, hash)
	}
}

func TestReadFirmwareReleaseInfoFileMissing(t *testing.T) {
	t.Parallel()

	_, _, err := readFirmwareReleaseInfoFile(filepath.Join(tempFirmwareReleaseFileDir(t), "missing.version"))
	if err == nil {
		t.Fatal("expected readFirmwareReleaseInfoFile to fail")
	}
}

func tempFirmwareReleaseFileDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "gornodeconf-release-file-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
