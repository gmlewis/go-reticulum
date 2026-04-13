// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

const tempDirPrefix = "gornpkg-test-"

func TestProgramSetupUsesVerbosityMinusQuietness(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	logger := rns.NewLogger()
	var originalLevel = logger.GetLogLevel()
	var originalDest = logger.GetLogDest()
	t.Cleanup(func() {
		logger.SetLogLevel(originalLevel)
		logger.SetLogDest(originalDest)
	})

	var capturedLevel int
	var capturedDest int
	var capturedConfigDir string
	rt := &runtimeT{
		app: &appT{
			configDir: tmpDir,
			verbose:   3,
			quiet:     1,
		},
		logger: logger,
		newReticulum: func(ts rns.Transport, configDir string, logger *rns.Logger) (*rns.Reticulum, error) {
			capturedLevel = logger.GetLogLevel()
			capturedDest = logger.GetLogDest()
			capturedConfigDir = configDir
			return &rns.Reticulum{}, nil
		},
	}
	if err := rt.programSetup(); err != nil {
		t.Fatalf("programSetup returned error: %v", err)
	}
	if got, want := capturedLevel, 2; got != want {
		t.Fatalf("log level = %v, want %v", got, want)
	}
	if got, want := capturedDest, rns.LogStdout; got != want {
		t.Fatalf("log dest = %v, want %v", got, want)
	}
	if capturedConfigDir != tmpDir {
		t.Fatalf("config dir = %q, want %q", capturedConfigDir, tmpDir)
	}
}

func TestProgramSetupForwardsConfigDirAndClosesReticulum(t *testing.T) {
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	if err := os.WriteFile(filepath.Join(tmpDir, "config"), []byte("[reticulum]\nshare_instance = No\n"), 0o600); err != nil {
		t.Fatalf("write config error: %v", err)
	}
	logger := rns.NewLogger()

	rt := &runtimeT{
		app: &appT{
			configDir: tmpDir,
			verbose:   0,
			quiet:     0,
		},
		logger:       logger,
		newReticulum: rns.NewReticulumWithLogger,
	}
	if err := rt.programSetup(); err != nil {
		t.Fatalf("first programSetup returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "config")); err != nil {
		t.Fatalf("expected config file to be created in %v: %v", tmpDir, err)
	}
	if err := rt.programSetup(); err != nil {
		t.Fatalf("second programSetup returned error: %v", err)
	}
}

func TestNewRuntime(t *testing.T) {
	t.Parallel()

	rt := newRuntime(nil)
	if rt == nil {
		t.Fatal("newRuntime() returned nil")
	}
	if rt.app == nil {
		t.Fatal("newRuntime() did not initialize the app state")
	}
	if rt.logger == nil {
		t.Fatal("newRuntime() did not initialize a logger")
	}
}
