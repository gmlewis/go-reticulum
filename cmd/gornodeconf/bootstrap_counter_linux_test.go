// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestNextBootstrapSerialNumberDefaultsToOne(t *testing.T) {
	t.Parallel()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-bootstrap-counter-*")
	t.Cleanup(cleanup)

	got, err := nextBootstrapSerialNumber(dir)
	if err != nil {
		t.Fatalf("nextBootstrapSerialNumber returned error: %v", err)
	}
	if got != 1 {
		t.Fatalf("serial mismatch: got %v want 1", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "firmware", "serial.counter"))
	if err != nil {
		t.Fatalf("read counter file: %v", err)
	}
	if string(data) != "1" {
		t.Fatalf("counter file mismatch: got %q want %q", data, "1")
	}
}

func TestNextBootstrapSerialNumberIncrementsExistingValue(t *testing.T) {
	t.Parallel()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-bootstrap-counter-*")
	t.Cleanup(cleanup)
	counterPath := filepath.Join(dir, "firmware", "serial.counter")
	if err := os.MkdirAll(filepath.Dir(counterPath), 0o755); err != nil {
		t.Fatalf("mkdir firmware dir: %v", err)
	}
	if err := os.WriteFile(counterPath, []byte("41\n"), 0o644); err != nil {
		t.Fatalf("write counter file: %v", err)
	}

	got, err := nextBootstrapSerialNumber(dir)
	if err != nil {
		t.Fatalf("nextBootstrapSerialNumber returned error: %v", err)
	}
	if got != 42 {
		t.Fatalf("serial mismatch: got %v want 42", got)
	}
	data, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatalf("read counter file: %v", err)
	}
	if string(data) != "42" {
		t.Fatalf("counter file mismatch: got %q want %q", data, "42")
	}
}
