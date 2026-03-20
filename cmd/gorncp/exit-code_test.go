// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestInvalidIdentityHashExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantExitCode int
	}{
		{"too short", []string{"-a", "abc123"}, 1},
		{"invalid hex", []string{"-a", "gggggggggggggggggggggggggggggggg"}, 1},
		{"empty", []string{"-a", ""}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("go", append([]string{"run", "."}, tt.args...)...)
			cmd.Stdin = nil
			cmd.Stdout = nil
			cmd.Stderr = nil
			err := cmd.Run()
			if exitErr, ok := err.(*exec.ExitError); ok {
				got := exitErr.ExitCode()
				if got != tt.wantExitCode {
					t.Errorf("exit code = %d, want %d", got, tt.wantExitCode)
				}
			} else {
				t.Errorf("exit code = 0 (no error), want %d", tt.wantExitCode)
			}
		})
	}
}

func TestMainExitCodeHelper(t *testing.T) {
	t.Parallel()

	// Test that os.Exit is called with correct codes
	tmpDir := tempDir(t)
	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")

	// This test verifies the exit code behavior
	cmd := exec.Command("go", "run", ".", "-a", "invalid")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if got := exitErr.ExitCode(); got != 1 {
			t.Errorf("invalid hash exit code = %d, want 1", got)
		}
	} else {
		t.Error("expected exit error for invalid hash")
	}
}

func tempDir(t *testing.T) string {
	t.Helper()
	var dir string
	var err error
	if os.PathSeparator == '/' && os.Getenv("GOOS") == "darwin" {
		dir, err = os.MkdirTemp("/tmp", "gorncp-test-*")
	} else {
		dir, err = os.MkdirTemp("", "gorncp-test-*")
	}
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("failed to remove temp dir: %v", err)
		}
	})
	return dir
}
