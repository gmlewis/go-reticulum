// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAnEmptyConfigdirInTheFileDoesNotEraseTheCommandLine pins the precedence
// rule for configdir: a NON-empty value in the file wins over the command line
// (TestBuildConfigPrecedence pins that), while an EMPTY value means "I have
// nothing to say" and must not erase what the operator passed.
//
// The bootstrapped config file contains `configdir = ""`. If that erased an
// explicit --configdir, the hub would let Reticulum choose its default
// directory — a different Reticulum instance from the one its clients are
// attached to, so no client could ever hear the hub announce.
func TestAnEmptyConfigdirInTheFileDoesNotEraseTheCommandLine(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	clientDir := filepath.Join(dir, "client")
	if err := os.MkdirAll(clientDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Exactly what the bootstrap writes, empty value and all.
	configPath := filepath.Join(dir, "rrcd.toml")
	contents := "[hub]\n" +
		"# Optional: Reticulum configuration directory.\n" +
		"configdir = \"\"\n" +
		"identity_path = \"" + filepath.Join(dir, "hub_identity") + "\"\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	opts := &gorrcdOptions{configdir: &clientDir}
	cfg, err := buildConfig(opts, configPath, filepath.Join(dir, "hub_identity"), filepath.Join(dir, "rooms.toml"))
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Configdir == nil {
		t.Fatal("an explicitly provided --configdir was erased by the file's empty configdir, " +
			"so the hub would attach to a different Reticulum instance than its clients")
	}
	if *cfg.Configdir != clientDir {
		t.Fatalf("Configdir = %q, want the command line's %q", *cfg.Configdir, clientDir)
	}
}

// TestAnAbsentConfigdirStillLetsTheFileDecide asserts the rule is narrow: with no
// --configdir on the command line, the file's value is the one that counts, including
// the empty value that means "let Reticulum choose".
func TestAnAbsentConfigdirStillLetsTheFileDecide(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	fromFile := filepath.Join(dir, "from-file")
	configPath := filepath.Join(dir, "rrcd.toml")
	if err := os.WriteFile(configPath, []byte("[hub]\nconfigdir = \""+fromFile+"\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := buildConfig(&gorrcdOptions{}, configPath, filepath.Join(dir, "hub_identity"), filepath.Join(dir, "rooms.toml"))
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Configdir == nil || *cfg.Configdir != fromFile {
		t.Fatalf("Configdir = %v, want the file's %q", cfg.Configdir, fromFile)
	}

	emptyPath := filepath.Join(dir, "empty.toml")
	if err := os.WriteFile(emptyPath, []byte("[hub]\nconfigdir = \"\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err = buildConfig(&gorrcdOptions{}, emptyPath, filepath.Join(dir, "hub_identity"), filepath.Join(dir, "rooms.toml"))
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if cfg.Configdir != nil {
		t.Fatalf("an empty file value must mean unset, got %q", *cfg.Configdir)
	}
}
