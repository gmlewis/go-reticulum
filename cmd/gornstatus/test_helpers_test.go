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

func unitTempDir(t *testing.T) (string, func()) {
	t.Helper()
	baseDir := ""
	if runtime.GOOS == "darwin" {
		baseDir = "/tmp"
	}
	dir, err := os.MkdirTemp(baseDir, "gornstatus-test-")
	if err != nil {
		t.Fatalf("tempDir error: %v", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
	}
	return dir, cleanup
}

func unitTempDirWithConfig(t *testing.T) (string, func()) {
	t.Helper()
	dir, cleanup := unitTempDir(t)
	instanceName := filepath.Base(dir)
	config := "[reticulum]\nenable_transport = False\nshare_instance = Yes\ninstance_name = " + instanceName + "\n\n[logging]\nloglevel = 2\n"
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(config), 0o600); err != nil {
		cleanup()
		t.Fatalf("writeTestConfig: %v", err)
	}
	return dir, cleanup
}
