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

// TestResolveNodeConfigDir covers resolveNodeConfigDir's default resolution,
// mirroring ReticulumGitNode.__init__'s configdir fallback (server.py). The
// test is skipped when /etc/rngit exists, since that branch wins over HOME.
func TestResolveNodeConfigDir(t *testing.T) {
	if _, err := os.Stat("/etc/rngit"); !os.IsNotExist(err) {
		t.Skip("skipping: /etc/rngit exists on this machine")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Explicit --config DIR always wins.
	if got, err := resolveNodeConfigDir("/explicit"); err != nil || got != "/explicit" {
		t.Fatalf("resolveNodeConfigDir(/explicit) = %q, %v; want /explicit, nil", got, err)
	}

	// No config dirs anywhere: ~/.rngit is the default.
	want := filepath.Join(home, ".rngit")
	if got, err := resolveNodeConfigDir(""); err != nil || got != want {
		t.Fatalf("resolveNodeConfigDir() = %q, %v; want %q, nil", got, err, want)
	}

	// When ~/.config/rngit holds a config file, upstream selects
	// ~/.rngit/reticulum (server.py:2017-2018).
	alt := filepath.Join(home, ".config", "rngit")
	if err := os.MkdirAll(alt, 0o755); err != nil {
		t.Fatalf("could not create %v: %v", alt, err)
	}
	if got, err := resolveNodeConfigDir(""); err != nil || got != want {
		t.Fatalf("empty ~/.config/rngit dir: resolveNodeConfigDir() = %q, %v; want %q, nil", got, err, want)
	}
	if err := os.WriteFile(filepath.Join(alt, "config"), nil, 0o600); err != nil {
		t.Fatalf("could not write config file: %v", err)
	}
	want = filepath.Join(home, ".rngit", "reticulum")
	if got, err := resolveNodeConfigDir(""); err != nil || got != want {
		t.Fatalf("with ~/.config/rngit/config: resolveNodeConfigDir() = %q, %v; want %q, nil", got, err, want)
	}
}
