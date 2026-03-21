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
)

func TestResourceMetadataInOptions(t *testing.T) {
	t.Parallel()

	tmpDir, cleanup := tempDir(t)
	defer cleanup()

	// Create a test file
	testFile := filepath.Join(tmpDir, "test.txt")
	testContent := []byte("Hello, World!")
	if err := os.WriteFile(testFile, testContent, 0o644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Create metadata
	metadata := map[string][]byte{
		"name": []byte("test.txt"),
	}

	// Verify metadata structure
	if metadata["name"] == nil {
		t.Fatal("Expected 'name' key in metadata")
	}

	if string(metadata["name"]) != "test.txt" {
		t.Fatalf("Expected filename 'test.txt', got %q", string(metadata["name"]))
	}

	// Create ResourceOptions with metadata
	opts := rns.ResourceOptions{
		AutoCompress: false,
		Metadata:     metadata,
	}

	// Verify options contain metadata
	if opts.Metadata == nil {
		t.Fatal("Expected Metadata in ResourceOptions")
	}

	if opts.Metadata["name"] == nil {
		t.Fatal("Expected 'name' key in ResourceOptions.Metadata")
	}

	t.Logf("ResourceOptions created with metadata: %v", metadata)
}
