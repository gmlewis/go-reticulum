// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func tempSerialPort(t *testing.T) string {
	t.Helper()

	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "gornodeconf-port-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return filepath.Join(dir, "port")
}
