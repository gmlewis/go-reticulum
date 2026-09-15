// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
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

// TestDefaultNodeConfigFileEnablesStats asserts the generated default config
// turns on stats recording and grants the "s" (stats) permission to the
// public group, so a new node records and serves repository statistics
// without extra setup.
func TestDefaultNodeConfigFileEnablesStats(t *testing.T) {
	t.Parallel()

	for _, want := range []string{"record_stats = yes", "public = r:all, s:all"} {
		if !strings.Contains(defaultNodeConfigFile, want) {
			t.Errorf("defaultNodeConfigFile missing %q:\n%v", want, defaultNodeConfigFile)
		}
	}

	cfg, err := parseNodeConfig(defaultNodeConfigFile)
	if err != nil {
		t.Fatalf("parseNodeConfig(defaultNodeConfigFile) error: %v", err)
	}
	if !cfg.recordStats {
		t.Error("default config should enable record_stats")
	}
	hasStats := false
	for _, entry := range cfg.access["public"] {
		if entry == "s:all" {
			hasStats = true
		}
	}
	if !hasStats {
		t.Errorf("default public access = %v; want an s:all entry", cfg.access["public"])
	}
}

// TestLoadNodeConfigCreatesStatsEnabledDefault asserts a brand-new config file
// is created with stats recording and the stats permission on, and that the
// first run's effective config matches the file it wrote.
func TestLoadNodeConfigCreatesStatsEnabledDefault(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gorngit-config-")
	cfg, err := loadNodeConfig(dir)
	if err != nil {
		t.Fatalf("loadNodeConfig(%q) error: %v", dir, err)
	}
	if !cfg.recordStats {
		t.Error("fresh node config should enable record_stats")
	}

	written, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatalf("could not read generated config: %v", err)
	}
	for _, want := range []string{"record_stats = yes", "s:all"} {
		if !strings.Contains(string(written), want) {
			t.Errorf("generated config missing %q:\n%s", want, written)
		}
	}

	reloaded, err := loadNodeConfig(dir)
	if err != nil {
		t.Fatalf("second loadNodeConfig(%q) error: %v", dir, err)
	}
	if reloaded.recordStats != cfg.recordStats {
		t.Errorf("reloaded recordStats = %v; want %v", reloaded.recordStats, cfg.recordStats)
	}
	if len(reloaded.access["public"]) != len(cfg.access["public"]) {
		t.Errorf("reloaded public access = %v; want %v", reloaded.access["public"], cfg.access["public"])
	}
}

// TestParseNodeConfigRecordStats covers the ConfigObj-style boolean forms
// (as_bool accepts yes/no as well as true/false), mirroring __apply_config
// (server.py:2206), and confirms an absent key leaves recording off for a
// pre-existing config.
func TestParseNodeConfigRecordStats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"yes", "yes", true},
		{"no", "no", false},
		{"true", "true", true},
		{"false", "false", false},
		{"on", "on", true},
		{"off", "off", false},
		{"one", "1", true},
		{"zero", "0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := parseNodeConfig("[rngit]\nrecord_stats = " + tt.value + "\n")
			if err != nil {
				t.Fatalf("parseNodeConfig error: %v", err)
			}
			if cfg.recordStats != tt.want {
				t.Errorf("record_stats = %v parsed as %v; want %v", tt.value, cfg.recordStats, tt.want)
			}
		})
	}

	cfg, err := parseNodeConfig("[rngit]\nannounce_interval = 360\n")
	if err != nil {
		t.Fatalf("parseNodeConfig error: %v", err)
	}
	if cfg.recordStats {
		t.Error("absent record_stats should stay off for an existing config")
	}
}
