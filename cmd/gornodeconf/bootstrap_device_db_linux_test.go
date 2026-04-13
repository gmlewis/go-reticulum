// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestWriteDeviceIdentityBackupWritesSerialNamedFile(t *testing.T) {
	t.Parallel()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-device-db-*")
	t.Cleanup(cleanup)

	path, err := writeDeviceIdentityBackup(dir, 0x01020304, []byte{0xaa, 0xbb, 0xcc})
	if err != nil {
		t.Fatalf("writeDeviceIdentityBackup returned error: %v", err)
	}
	want := filepath.Join(dir, "firmware", "device_db", "01020304")
	if path != want {
		t.Fatalf("backup path mismatch: got %q want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read backup file: %v", err)
	}
	if string(data) != string([]byte{0xaa, 0xbb, 0xcc}) {
		t.Fatalf("backup content mismatch: got %x", data)
	}
}
