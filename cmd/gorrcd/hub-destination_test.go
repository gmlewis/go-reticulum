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

	"github.com/gmlewis/go-reticulum/rrc"
)

// TestPublishHubDestination asserts the hub writes its destination where a
// supervisor can read it, in the lowercase hexadecimal form every other Reticulum
// tool prints, and that a rewrite replaces the file rather than appending to it.
func TestPublishHubDestination(t *testing.T) {
	t.Parallel()

	identityPath := filepath.Join(tempDir(t), "hub_identity")
	hash := []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}

	if err := publishHubDestination(identityPath, hash); err != nil {
		t.Fatalf("publishHubDestination: %v", err)
	}
	path := hubDestinationPath(identityPath)
	if want := identityPath + ".rrc.hub"; path != want {
		t.Fatalf("hubDestinationPath = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	const want = "0123456789abcdef0123456789abcdef\n"
	if string(data) != want {
		t.Fatalf("published destination = %q, want %q", data, want)
	}

	// A second hub run must replace the value, not append a second one: a reader
	// that took the first line would dial a hub that no longer exists.
	second := []byte{0xFF, 0xEE, 0xDD, 0xCC, 0xBB, 0xAA, 0x99, 0x88,
		0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00}
	if err := publishHubDestination(identityPath, second); err != nil {
		t.Fatalf("publishHubDestination (second): %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Count(string(data), "\n") != 1 {
		t.Fatalf("published destination = %q, want exactly one line", data)
	}
	if want := "ffeeddccbbaa99887766554433221100\n"; string(data) != want {
		t.Fatalf("published destination = %q, want %q", data, want)
	}
}

// TestPublishHubDestinationWithoutAnIdentityIsANoOp asserts a hub configured with no
// identity path does not create a file beside the working directory.
func TestPublishHubDestinationWithoutAnIdentityIsANoOp(t *testing.T) {
	t.Parallel()

	if err := publishHubDestination("", []byte{0xAA}); err != nil {
		t.Fatalf("publishHubDestination with no identity: %v", err)
	}
	if hubDestinationPath("") != ".rrc.hub" {
		t.Fatalf("hubDestinationPath(\"\") = %q", hubDestinationPath(""))
	}
	if _, err := os.Stat(".rrc.hub"); !os.IsNotExist(err) {
		_ = os.Remove(".rrc.hub")
		t.Fatalf("a hub with no identity path wrote a destination file")
	}
}

// TestEffectiveIdentityPathFollowsTheConfiguration asserts the destination is published
// beside the identity the hub actually loaded. The TOML is applied over the path seeds, so
// `identity_path` in the file wins over --identity, and publishing beside the flag's value
// would put the file where the operator is not looking.
func TestEffectiveIdentityPathFollowsTheConfiguration(t *testing.T) {
	t.Parallel()

	fromFlag := "/from/flag/hub_identity"
	fromFile := "/from/file/hub_identity"

	if got := effectiveIdentityPath(rrc.HubConfig{}, fromFlag); got != fromFlag {
		t.Fatalf("with no configured path, effective = %q, want the flag's %q", got, fromFlag)
	}
	if got := effectiveIdentityPath(rrc.HubConfig{IdentityPath: &fromFile}, fromFlag); got != fromFile {
		t.Fatalf("effective = %q, want the configured %q", got, fromFile)
	}
	blank := "   "
	if got := effectiveIdentityPath(rrc.HubConfig{IdentityPath: &blank}, fromFlag); got != fromFlag {
		t.Fatalf("a blank configured path must fall back, got %q", got)
	}
}
