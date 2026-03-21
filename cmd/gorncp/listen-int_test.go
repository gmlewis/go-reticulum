// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestListenModeIdentityCreation(t *testing.T) {
	t.Parallel()

	// Create a temp directory for config
	tmpDir, cleanup := tempDir(t)
	defer cleanup()
	identityDir := filepath.Join(tmpDir, "identities")
	identityPath := filepath.Join(identityDir, AppName)

	// Verify identity doesn't exist initially
	if _, err := os.Stat(identityPath); err == nil {
		t.Fatal("Identity should not exist initially")
	}

	// Simulate what listen() does - create new identity if not found
	var id *rns.Identity
	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		// Create directory first (matches Python behavior)
		if err := os.MkdirAll(identityDir, 0o700); err != nil {
			t.Fatalf("Could not create identity directory: %v", err)
		}
		var err error
		id, err = rns.NewIdentity(true)
		if err != nil {
			t.Fatalf("Could not create identity: %v", err)
		}
		if err := id.ToFile(identityPath); err != nil {
			t.Fatalf("Could not persist identity %q: %v", identityPath, err)
		}
	}

	// Verify identity was created
	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		t.Fatal("Identity should be created")
	}

	// Verify identity can be loaded
	loadedID, err := rns.FromFile(identityPath)
	if err != nil {
		t.Fatalf("Identity should be loadable: %v", err)
	}

	if loadedID == nil {
		t.Fatal("Loaded identity should not be nil")
	}
}
