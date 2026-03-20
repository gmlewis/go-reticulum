// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
	mustTest(t, os.Setenv("HOME", tmpDir))
	defer func() { _ = os.Unsetenv("HOME") }()

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

func TestCorruptIdentityFileExitCode(t *testing.T) {
	t.Parallel()

	tmpDir := tempDir(t)
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	identityPath := filepath.Join(tmpDir, "identity")

	// Write corrupt data to identity file
	mustTest(t, os.WriteFile(identityPath, []byte("corrupt data"), 0o644))

	// Build the binary First
	buildCmd := exec.Command("go", "build", "-o", filepath.Join(tmpDir, "gorncp"), ".")
	buildCmd.Dir = "."
	buildCmd.Env = append(os.Environ(), "HOME="+tmpDir)
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Run the binary
	cmd := exec.Command(filepath.Join(tmpDir, "gorncp"), "-l", "-i", identityPath, "--config", configDir)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if got := exitErr.ExitCode(); got != 2 {
			t.Errorf("corrupt identity exit code = %d, want 2", got)
		}
	} else {
		t.Error("expected exit error for corrupt identity")
	}
}

func TestOutputDirectoryNotFoundExitCode(t *testing.T) {
	t.Parallel()

	tmpDir := tempDir(t)
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	// Create identity directory to avoid identity creation failure
	identityDir := filepath.Join(tmpDir, ".reticulum", "identities")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatalf("failed to create identity dir: %v", err)
	}

	// Build the binary First
	buildCmd := exec.Command("go", "build", "-o", filepath.Join(tmpDir, "gorncp"), ".")
	buildCmd.Dir = "."
	buildCmd.Env = append(os.Environ(), "HOME="+tmpDir)
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Run the binary with non-existent save directory
	nonExistentDir := "/nonexistent-gorncp-test-dir"
	cmd := exec.Command(filepath.Join(tmpDir, "gorncp"), "-l", "-s", nonExistentDir, "--config", configDir)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if got := exitErr.ExitCode(); got != 3 {
			t.Errorf("output directory not found exit code = %d, want 3", got)
		}
	} else {
		t.Error("expected exit error for non-existent output directory")
	}
}

func TestOutputDirectoryNotWritableExitCode(t *testing.T) {
	t.Parallel()

	tmpDir := tempDir(t)
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	// Create identity directory to avoid identity creation failure
	identityDir := filepath.Join(tmpDir, ".reticulum", "identities")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatalf("failed to create identity dir: %v", err)
	}

	// Create a read-only directory
	readOnlyDir := filepath.Join(tmpDir, "readonly")
	if err := os.MkdirAll(readOnlyDir, 0o555); err != nil {
		t.Fatalf("failed to create readonly dir: %v", err)
	}

	// Build the binary First
	buildCmd := exec.Command("go", "build", "-o", filepath.Join(tmpDir, "gorncp"), ".")
	buildCmd.Dir = "."
	buildCmd.Env = append(os.Environ(), "HOME="+tmpDir)
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	// Run the binary with read-only save directory
	cmd := exec.Command(filepath.Join(tmpDir, "gorncp"), "-l", "-s", readOnlyDir, "--config", configDir)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if got := exitErr.ExitCode(); got != 4 {
			t.Errorf("output directory not writable exit code = %d, want 4", got)
		}
	} else {
		t.Error("expected exit error for non-writable output directory")
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
