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
	originalLevel := rns.GetLogLevel()
	originalDest := rns.GetLogDest()
	t.Cleanup(func() {
		rns.SetLogLevel(originalLevel)
		rns.SetLogDest(originalDest)
	})

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	var capturedLevel int
	var capturedDest int
	var capturedConfigDir string
	if err := programSetup(tmpDir, 3, 1, func(ts rns.Transport, configDir string) (*rns.Reticulum, error) {
		capturedLevel = rns.GetLogLevel()
		capturedDest = rns.GetLogDest()
		capturedConfigDir = configDir
		return &rns.Reticulum{}, nil
	}); err != nil {
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
	originalLevel := rns.GetLogLevel()
	originalDest := rns.GetLogDest()
	t.Cleanup(func() {
		rns.SetLogLevel(originalLevel)
		rns.SetLogDest(originalDest)
	})

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	if err := programSetup(tmpDir, 0, 0, rns.NewReticulum); err != nil {
		t.Fatalf("first programSetup returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "config")); err != nil {
		t.Fatalf("expected config file to be created in %v: %v", tmpDir, err)
	}
	if err := programSetup(tmpDir, 0, 0, rns.NewReticulum); err != nil {
		t.Fatalf("second programSetup returned error: %v", err)
	}
}
