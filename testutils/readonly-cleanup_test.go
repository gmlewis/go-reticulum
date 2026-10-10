// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTempDirCleansUpAReadOnlyTree pins the cleanup against the one shape of tree
// it cannot remove with a plain os.RemoveAll: the Go module cache's.
//
// Every module the toolchain downloads is left 0444 inside 0555, and a scratch
// GOPATH a test built holds exactly that. A directory without write permission
// cannot give up the entries inside it, so removal fails with EACCES — which is how
// the first dependency this module ever took failed a test in a package that never
// mentions it, in cleanup, with a message naming a file in someone else's module.
func TestTempDirCleansUpAReadOnlyTree(t *testing.T) {
	dir := TempDir(t, "readonly-cleanup-")

	module := filepath.Join(dir, "go", "pkg", "mod", "example.com", "dep@v1.0.0", "plan9")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatalf("building a module cache to clean up: %v", err)
	}
	if err := os.WriteFile(filepath.Join(module, "mkall.sh"), []byte("#!/bin/sh\n"), 0o444); err != nil {
		t.Fatalf("writing a read-only module file: %v", err)
	}
	// The modes the module cache sets, in the order it sets them: the file is made
	// read-only, and then the directory it lives in loses its write bit.
	if err := os.Chmod(module, 0o555); err != nil {
		t.Fatalf("making the module directory read-only: %v", err)
	}
}
