// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

var discard io.Writer = io.Discard

// TestResolveRnshConfigDirDefault covers the v1.3.9+ rnsh config-directory
// resolution that mirrors Python's ensure_config_directory (RNS/Utilities/
// rnsh/rnsh.py:89-108): an explicit --config dir wins; otherwise ~/.config/
// rnsh is used when it exists, then ~/.rnsh when it exists, and finally
// ~/.rnsh is used as the created fallback.
func TestResolveRnshConfigDirDefault(t *testing.T) {
	t.Parallel()
	tmpHome := testutils.TempDir(t, tempDirPrefix)

	t.Run("explicit config dir wins", func(t *testing.T) {
		t.Parallel()
		explicit := filepath.Join(tmpHome, "my-rnsh")
		if got := resolveRnshConfigDir(explicit, tmpHome); got != explicit {
			t.Errorf("resolveRnshConfigDir(%q, %q) = %q, want %q", explicit, tmpHome, got, explicit)
		}
	})

	t.Run("xdg config dir preferred when present", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(tmpHome, "xdg-home")
		xdg := filepath.Join(home, ".config", "rnsh")
		mustMkdirAll(t, xdg)
		dotRnsh := filepath.Join(home, ".rnsh")
		mustMkdirAll(t, dotRnsh)
		if got := resolveRnshConfigDir("", home); got != xdg {
			t.Errorf("resolveRnshConfigDir(%q, %q) = %q, want %q", "", home, got, xdg)
		}
	})

	t.Run("dot rnsh used when xdg absent", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(tmpHome, "dot-home")
		dotRnsh := filepath.Join(home, ".rnsh")
		mustMkdirAll(t, dotRnsh)
		if got := resolveRnshConfigDir("", home); got != dotRnsh {
			t.Errorf("resolveRnshConfigDir(%q, %q) = %q, want %q", "", home, got, dotRnsh)
		}
	})

	t.Run("falls back to dot rnsh when neither exists", func(t *testing.T) {
		t.Parallel()
		home := filepath.Join(tmpHome, "empty-home")
		want := filepath.Join(home, ".rnsh")
		if got := resolveRnshConfigDir("", home); got != want {
			t.Errorf("resolveRnshConfigDir(%q, %q) = %q, want %q", "", home, got, want)
		}
	})
}

// TestResolveIdentityPathDefault covers the v1.3.9+ default identity location
// (RNS/Utilities/rnsh/rnsh.py:59-62): the identity lives at
// <rnsh-config-dir>/identity for the initiator and
// <rnsh-config-dir>/identity.<service> for the listener (default service
// "default" -> identity.default). An explicit -i/--identity path always wins.
func TestResolveIdentityPathDefault(t *testing.T) {
	t.Parallel()
	tmpHome := testutils.TempDir(t, tempDirPrefix)
	rnshDir := filepath.Join(tmpHome, ".rnsh")
	mustMkdirAll(t, rnshDir)

	t.Run("initiator uses identity without service suffix", func(t *testing.T) {
		t.Parallel()
		opts := options{}
		got, err := resolveIdentityPathCustom(opts, tmpHome)
		if err != nil {
			t.Fatalf("resolveIdentityPathCustom: %v", err)
		}
		want := filepath.Join(rnshDir, "identity")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("listener with default service uses identity.default", func(t *testing.T) {
		t.Parallel()
		opts := options{listen: true, serviceName: defaultServiceName}
		got, err := resolveIdentityPathCustom(opts, tmpHome)
		if err != nil {
			t.Fatalf("resolveIdentityPathCustom: %v", err)
		}
		want := filepath.Join(rnshDir, "identity.default")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("listener with explicit service uses identity.service", func(t *testing.T) {
		t.Parallel()
		opts := options{listen: true, serviceName: "relay"}
		got, err := resolveIdentityPathCustom(opts, tmpHome)
		if err != nil {
			t.Fatalf("resolveIdentityPathCustom: %v", err)
		}
		want := filepath.Join(rnshDir, "identity.relay")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("explicit identity path wins", func(t *testing.T) {
		t.Parallel()
		explicit := filepath.Join(tmpHome, "custom.id")
		opts := options{identityPath: explicit, listen: true, serviceName: defaultServiceName}
		got, err := resolveIdentityPathCustom(opts, tmpHome)
		if err != nil {
			t.Fatalf("resolveIdentityPathCustom: %v", err)
		}
		if got != explicit {
			t.Errorf("got %q, want %q", got, explicit)
		}
	})

	t.Run("explicit rnsh config dir overrides home default", func(t *testing.T) {
		t.Parallel()
		customDir := filepath.Join(tmpHome, "alt-rnsh")
		mustMkdirAll(t, customDir)
		opts := options{rnshConfigDir: customDir, listen: true, serviceName: defaultServiceName}
		got, err := resolveIdentityPathCustom(opts, tmpHome)
		if err != nil {
			t.Fatalf("resolveIdentityPathCustom: %v", err)
		}
		want := filepath.Join(customDir, "identity.default")
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestFlagMappingRnsConfigVsConfig covers the v1.3.9+ flag split: -c and
// --rnsconfig select the Reticulum config directory, while --config selects
// the rnsh config directory.
func TestFlagMappingRnsConfigVsConfig(t *testing.T) {
	t.Parallel()

	t.Run("rnsconfig long form sets RNS config dir", func(t *testing.T) {
		t.Parallel()
		opts, err := parseFlags([]string{"--rnsconfig", "/rns", "-p"}, discard)
		if err != nil {
			t.Fatalf("parseFlags: %v", err)
		}
		if opts.configDir != "/rns" {
			t.Errorf("configDir = %q, want %q", opts.configDir, "/rns")
		}
		if opts.rnshConfigDir != "" {
			t.Errorf("rnshConfigDir = %q, want empty", opts.rnshConfigDir)
		}
	})

	t.Run("short -c sets RNS config dir", func(t *testing.T) {
		t.Parallel()
		opts, err := parseFlags([]string{"-c", "/rns2", "-p"}, discard)
		if err != nil {
			t.Fatalf("parseFlags: %v", err)
		}
		if opts.configDir != "/rns2" {
			t.Errorf("configDir = %q, want %q", opts.configDir, "/rns2")
		}
	})

	t.Run("config sets rnsh config dir", func(t *testing.T) {
		t.Parallel()
		opts, err := parseFlags([]string{"--config", "/rnsh", "--rnsconfig", "/rns", "-p"}, discard)
		if err != nil {
			t.Fatalf("parseFlags: %v", err)
		}
		if opts.rnshConfigDir != "/rnsh" {
			t.Errorf("rnshConfigDir = %q, want %q", opts.rnshConfigDir, "/rnsh")
		}
		if opts.configDir != "/rns" {
			t.Errorf("configDir = %q, want %q", opts.configDir, "/rns")
		}
	})
}

func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll %q: %v", dir, err)
	}
}
