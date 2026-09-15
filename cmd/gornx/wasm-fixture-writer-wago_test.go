// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file holds the fixture-writing helper for the wago plugin tests. It
// carries the same build constraint as its callers: only the wago-tagged
// plugin tests write wasm fixtures, so an untagged copy would be dead code
// (and unused, U1000) without the tag.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFixtureFile writes fixture bytes under dir with the given plugin file
// name (which also names the command it serves) and returns the path.
func writeFixtureFile(t *testing.T, dir, name string, wasm []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, wasm, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
