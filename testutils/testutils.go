// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package testutils provides shared helper functions for tests across this
// repository.
package testutils

import (
	"os"
	"runtime"
	"testing"
)

func tempBaseDir() string {
	if runtime.GOOS == "darwin" {
		return "/tmp"
	}
	return ""
}

// TempDir creates a temporary directory for a test and returns a cleanup
// function that removes it.
func TempDir(t *testing.T, prefix string) (string, func()) {
	t.Helper()

	dir, err := os.MkdirTemp(tempBaseDir(), prefix)
	if err != nil {
		t.Fatalf("TempDir error: %v", err)
	}

	cleanup := func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("os.RemoveAll: %v", err)
		}
	}

	return dir, cleanup
}
