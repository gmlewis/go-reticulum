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

	"github.com/gmlewis/go-reticulum/rns"
)

// writeIdentity writes a real 64-byte Reticulum private key to path and returns
// its identity hash.
func writeIdentity(t *testing.T, path string) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	identity, err := rns.NewIdentity(true, silentLogger())
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	if err := identity.ToFile(path); err != nil {
		t.Fatalf("ToFile(%v): %v", path, err)
	}
	return identity.Hash
}

// TestLoadIdentityFromConfigDir asserts the tool reuses the identity Reticulum
// already keeps in the configuration directory.
func TestLoadIdentityFromConfigDir(t *testing.T) {
	t.Parallel()

	configDir := tempDir(t)
	want := writeIdentity(t, filepath.Join(configDir, reticulumStorageDir, transportIdentityName))

	identity, path, err := loadIdentity(configDir, "")
	if err != nil {
		t.Fatalf("loadIdentity: %v", err)
	}
	if got := hexString(identity.Hash); got != hexString(want) {
		t.Errorf("identity hash = %v, want %v", got, hexString(want))
	}
	if wantPath := filepath.Join(configDir, reticulumStorageDir, transportIdentityName); path != wantPath {
		t.Errorf("path = %v, want %v", path, wantPath)
	}
}

// TestLoadIdentityUsesExplicitPath asserts --identity wins over the directory
// identity.
func TestLoadIdentityUsesExplicitPath(t *testing.T) {
	t.Parallel()

	configDir := tempDir(t)
	writeIdentity(t, filepath.Join(configDir, reticulumStorageDir, transportIdentityName))
	explicit := filepath.Join(tempDir(t), "chosen_identity")
	want := writeIdentity(t, explicit)

	identity, path, err := loadIdentity(configDir, explicit)
	if err != nil {
		t.Fatalf("loadIdentity: %v", err)
	}
	if got := hexString(identity.Hash); got != hexString(want) {
		t.Errorf("identity hash = %v, want the explicit identity %v", got, hexString(want))
	}
	if path != explicit {
		t.Errorf("path = %v, want %v", path, explicit)
	}
}

// TestLoadIdentityNeverCreates asserts a missing identity is an error naming
// every path that was searched: silently generating one would give the tool a
// different identity hash on every run.
func TestLoadIdentityNeverCreates(t *testing.T) {
	t.Parallel()

	configDir := tempDir(t)
	_, _, err := loadIdentity(configDir, "")
	if err == nil {
		t.Fatal("loadIdentity with no identity = nil error, want a failure")
	}
	wantPath := filepath.Join(configDir, reticulumStorageDir, transportIdentityName)
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("error = %q, want it to name %v", err, wantPath)
	}
	if !strings.Contains(err.Error(), "--identity") {
		t.Errorf("error = %q, want it to mention --identity", err)
	}
	if _, statErr := os.Stat(wantPath); !os.IsNotExist(statErr) {
		t.Errorf("loadIdentity created %v; it must never create one", wantPath)
	}
}

// TestLoadIdentityExplicitMissingDoesNotFallBack asserts an explicit path that
// does not exist fails instead of quietly using the directory identity.
func TestLoadIdentityExplicitMissingDoesNotFallBack(t *testing.T) {
	t.Parallel()

	configDir := tempDir(t)
	writeIdentity(t, filepath.Join(configDir, reticulumStorageDir, transportIdentityName))

	_, _, err := loadIdentity(configDir, filepath.Join(configDir, "absent"))
	if err == nil {
		t.Fatal("loadIdentity with an absent explicit path = nil error, want a failure")
	}
}

// TestLoadIdentityRejectsCorruptFile asserts a file that exists but does not
// hold key material is reported rather than replaced.
func TestLoadIdentityRejectsCorruptFile(t *testing.T) {
	t.Parallel()

	configDir := tempDir(t)
	path := filepath.Join(configDir, reticulumStorageDir, transportIdentityName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("not a key"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err := loadIdentity(configDir, "")
	if err == nil {
		t.Fatal("loadIdentity with a corrupt identity = nil error, want a failure")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %q, want it to name %v", err, path)
	}
}

// TestIdentityCandidates asserts an explicit identity replaces the search list
// and the default list names the transport identity.
func TestIdentityCandidates(t *testing.T) {
	t.Parallel()

	if got := identityCandidates("/rns", "/chosen"); len(got) != 1 || got[0] != "/chosen" {
		t.Errorf("identityCandidates with an override = %v, want [/chosen]", got)
	}
	want := filepath.Join("/rns", reticulumStorageDir, transportIdentityName)
	if got := identityCandidates("/rns", ""); len(got) != 1 || got[0] != want {
		t.Errorf("identityCandidates default = %v, want [%v]", got, want)
	}
}

// TestReticulumConfigDir asserts --config wins and the default is ~/.reticulum.
// It sets HOME, so it does not run in parallel with anything else.
func TestReticulumConfigDir(t *testing.T) {
	got, err := reticulumConfigDir("/alt/rns")
	if err != nil {
		t.Fatalf("reticulumConfigDir: %v", err)
	}
	if got != "/alt/rns" {
		t.Errorf("reticulumConfigDir(override) = %q, want %q", got, "/alt/rns")
	}

	home := tempDir(t)
	t.Setenv("HOME", home)
	got, err = reticulumConfigDir("")
	if err != nil {
		t.Fatalf("reticulumConfigDir: %v", err)
	}
	if want := filepath.Join(home, defaultConfigDirName); got != want {
		t.Errorf("reticulumConfigDir(\"\") = %q, want %q", got, want)
	}
}

// TestExpandUser asserts a leading tilde resolves against the home directory
// and every other path is returned unchanged. It sets HOME, so it does not run
// in parallel with anything else.
func TestExpandUser(t *testing.T) {
	home := tempDir(t)
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "absolute", in: "/etc/hosts", want: "/etc/hosts"},
		{name: "relative", in: "config", want: "config"},
		{name: "tilde alone", in: "~", want: home},
		{name: "tilde path", in: "~/.reticulum/storage/transport_identity",
			want: filepath.Join(home, ".reticulum/storage/transport_identity")},
		{name: "trimmed", in: "  /etc/hosts  ", want: "/etc/hosts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandUser(tt.in)
			if err != nil {
				t.Fatalf("expandUser(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("expandUser(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
