// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestInvalidIdentityHashExitCode(t *testing.T) {

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

func TestMalformedDestinationHashExitCode(t *testing.T) {

	tests := []struct {
		name     string
		destHash string
		wantMsg  string
	}{
		{
			name:     "too short",
			destHash: "abc123",
			wantMsg:  "Allowed destination length is invalid, must be 32 hexadecimal characters (16 bytes).",
		},
		{
			name:     "invalid hex",
			destHash: "gggggggggggggggggggggggggggggggg",
			wantMsg:  "Invalid destination entered. Check your input.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
			defer cleanup()

			configDir := filepath.Join(tmpDir, "config")
			if err := os.MkdirAll(configDir, 0o755); err != nil {
				t.Fatalf("failed to create config dir: %v", err)
			}

			identityPath := filepath.Join(tmpDir, "identity")
			_ = os.Remove(identityPath)

			inputFile := filepath.Join(tmpDir, "input.txt")
			if err := os.WriteFile(inputFile, []byte("test input"), 0o644); err != nil {
				t.Fatalf("failed to create input file: %v", err)
			}

			cmd := exec.Command("go", "run", ".", "-config", configDir, "-i", identityPath, tt.destHash, inputFile)
			cmd.Dir = "."
			cmd.Env = append(os.Environ(), "HOME="+tmpDir)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected malformed destination hash to fail, output: %s", string(out))
			}
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("expected exit error, got %T: %v", err, err)
			}
			if got := exitErr.ExitCode(); got != 1 {
				t.Fatalf("exit code = %d, want 1; output: %s", got, string(out))
			}
			if !strings.Contains(string(out), tt.wantMsg) {
				t.Fatalf("output does not contain %q: %s", tt.wantMsg, string(out))
			}
		})
	}
}

func TestMissingSendFileExitCode(t *testing.T) {

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()

	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	identityPath := filepath.Join(tmpDir, "identity")
	missingFile := filepath.Join(tmpDir, "missing.txt")
	destHash := "0123456789abcdef0123456789abcdef"

	cmd := exec.Command("go", "run", ".", "-config", configDir, "-i", identityPath, destHash, missingFile)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "HOME="+tmpDir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected missing send file to fail, output: %s", string(out))
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected exit error, got %T: %v", err, err)
	}
	if got := exitErr.ExitCode(); got != 1 {
		t.Fatalf("exit code = %d, want 1; output: %s", got, string(out))
	}
	if !strings.Contains(string(out), "File not found") {
		t.Fatalf("output does not contain %q: %s", "File not found", string(out))
	}
}

func TestMainExitCodeHelper(t *testing.T) {

	// Test that os.Exit is called with correct codes
	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
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

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	identityPath := filepath.Join(tmpDir, "identity")

	// Write corrupt data to identity file
	mustTest(t, os.WriteFile(identityPath, []byte("corrupt data"), 0o644))

	// Build the binary first (go run doesn't preserve exit codes reliably)
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
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if got := exitErr.ExitCode(); got != 2 {
			t.Errorf("corrupt identity exit code = %d, want 2", got)
		}
	} else {
		t.Error("expected exit error for corrupt identity")
	}
	if !strings.Contains(string(out), "may be corrupt or unreadable") {
		t.Fatalf("output does not contain corrupt-identity message: %s", string(out))
	}
}

func TestOutputDirectoryNotFoundExitCode(t *testing.T) {

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	// Create identity directory to avoid identity creation failure
	identityDir := filepath.Join(tmpDir, ".reticulum", "identities")
	if err := os.MkdirAll(identityDir, 0o755); err != nil {
		t.Fatalf("failed to create identity dir: %v", err)
	}

	// Build the binary first (go run doesn't preserve exit codes reliably)
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
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if got := exitErr.ExitCode(); got != 3 {
			t.Errorf("output directory not found exit code = %d, want 3", got)
		}
	} else {
		t.Error("expected exit error for non-existent output directory")
	}
	if !strings.Contains(string(out), "Output directory not found") {
		t.Fatalf("output does not contain output-directory message: %s", string(out))
	}
}

func TestOutputDirectoryNotWritableExitCode(t *testing.T) {

	tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
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

	// Build the binary first (go run doesn't preserve exit codes reliably)
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
	out, err := cmd.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok {
		if got := exitErr.ExitCode(); got != 4 {
			t.Errorf("output directory not writable exit code = %d, want 4", got)
		}
	} else {
		t.Error("expected exit error for non-writable output directory")
	}
	if !strings.Contains(string(out), "Output directory not writable") {
		t.Fatalf("output does not contain output-directory message: %s", string(out))
	}
}
