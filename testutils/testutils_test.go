// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTempDirCreatesAndCleansUpDirectory(t *testing.T) {
	t.Parallel()

	dir, cleanup := TempDir(t, "testutils-tempdir-")

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("TempDir returned missing directory %q: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("TempDir returned non-directory path %q", dir)
	}
	if !strings.Contains(filepath.Base(dir), "testutils-tempdir-") {
		t.Fatalf("TempDir directory name %q does not contain prefix", filepath.Base(dir))
	}

	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("TempDir cleanup left directory behind: %v", err)
	}
}

func TestTempDirWithConfigCreatesConfigFile(t *testing.T) {
	t.Parallel()

	config := "[reticulum]\ninstance_name = test\n"
	dir, cleanup := TempDirWithConfig(t, "testutils-config-", func(string) string { return config })

	configPath := filepath.Join(dir, "config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("TempDirWithConfig missing config file %q: %v", configPath, err)
	}
	if got := string(data); got != config {
		t.Fatalf("TempDirWithConfig config = %q, want %q", got, config)
	}

	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("TempDirWithConfig cleanup left directory behind: %v", err)
	}
}

func TestTempBaseDir(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "darwin" {
		if got := tempBaseDir(); got != "/tmp" {
			t.Fatalf("tempBaseDir() = %q, want %q", got, "/tmp")
		}
		return
	}

	if got := tempBaseDir(); got != "" {
		t.Fatalf("tempBaseDir() = %q, want empty on %v", got, runtime.GOOS)
	}
}

func TestNormalizeOutput(t *testing.T) {
	t.Parallel()

	if got := NormalizeOutput("gornprobe\r\n\b  hello\nworld\t  42"); got != "gornprobe hello world 42" {
		t.Fatalf("NormalizeOutput = %q", got)
	}

	if got := NormalizeOutput("   multiple    spaces\n\n  preserved   "); got != "multiple spaces preserved" {
		t.Fatalf("NormalizeOutput = %q", got)
	}
}
