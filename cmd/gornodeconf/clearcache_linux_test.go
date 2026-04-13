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

func TestClearCacheDeletesUpdateDirectory(t *testing.T) {
	t.Parallel()

	home := tempClearCacheHome(t)
	updateDir := filepath.Join(home, ".config", "rnodeconf", "update")
	if err := os.MkdirAll(updateDir, 0o755); err != nil {
		t.Fatalf("create update dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(updateDir, "cache.bin"), []byte("cache"), 0o644); err != nil {
		t.Fatalf("write cache file: %v", err)
	}

	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "--clear-cache")
	if err != nil {
		t.Fatalf("gornodeconf --clear-cache failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "Clearing local firmware cache...") {
		t.Fatalf("missing clear-cache banner: %v", out)
	}
	if !strings.Contains(out, "Done") {
		t.Fatalf("missing clear-cache completion message: %v", out)
	}
	if _, err := os.Stat(updateDir); !os.IsNotExist(err) {
		t.Fatalf("expected update dir to be removed, stat err=%v", err)
	}
}

func TestClearCacheFailsWhenUpdateDirectoryIsMissing(t *testing.T) {
	t.Parallel()

	home := tempClearCacheHome(t)
	out, err := runGornodeconfWithEnv(map[string]string{"HOME": home}, "--clear-cache")
	if err == nil {
		t.Fatal("expected gornodeconf --clear-cache to fail without a cache directory")
	}
	if !strings.Contains(out, "Clearing local firmware cache...") {
		t.Fatalf("missing clear-cache banner: %v", out)
	}
}

func tempClearCacheHome(t *testing.T) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-clearcache-*")
	t.Cleanup(cleanup)
	return dir
}
