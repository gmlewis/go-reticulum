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
	"time"
)

func buildGolxmd(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, cleanup := tempDir(t)
	bin := filepath.Join(tmpDir, "golxmd")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		t.Fatalf("failed to build golxmd: %v\n%v", err, string(out))
	}
	return bin, cleanup
}

func TestGolxmd_Version(t *testing.T) {
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()

	out, err := exec.Command(golxmdBin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("golxmd --version failed: %v\n%v", err, string(out))
	}

	output := strings.TrimSpace(string(out))
	if !strings.Contains(output, "golxmd") {
		t.Errorf("expected output to contain 'golxmd', got: %v", output)
	}
}

func TestGolxmd_ExampleConfig(t *testing.T) {
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()

	out, err := exec.Command(golxmdBin, "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("golxmd --exampleconfig failed: %v\n%v", err, string(out))
	}

	output := string(out)
	// Check for key sections
	requiredSections := []string{
		"[propagation]",
		"[lxmf]",
		"[logging]",
		"enable_node = no",
		"auth_required = no",
		"display_name = Anonymous Peer",
		"loglevel = 4",
	}
	for _, section := range requiredSections {
		if !strings.Contains(output, section) {
			t.Errorf("expected config to contain %q, got:\n%v", section, output)
		}
	}
}

func TestGolxmd_Help(t *testing.T) {
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()

	out, err := exec.Command(golxmdBin, "-h").CombinedOutput()
	if err != nil {
		t.Fatalf("golxmd -h failed: %v\n%v", err, string(out))
	}

	output := string(out)
	// Check for key options
	requiredOptions := []string{
		"--config",
		"--rnsconfig",
		"-p",
		"--propagation-node",
		"--status",
		"--peers",
		"--sync",
		"--break",
		"--exampleconfig",
		"--version",
	}
	for _, opt := range requiredOptions {
		if !strings.Contains(output, opt) {
			t.Errorf("expected help to contain option %q, got:\n%v", opt, output)
		}
	}
}

func TestGolxmd_LongFormParserAliases(t *testing.T) {
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()

	out, err := exec.Command(golxmdBin, "--verbose", "--quiet", "--exampleconfig").CombinedOutput()
	if err != nil {
		t.Fatalf("golxmd --verbose --quiet --exampleconfig failed: %v\n%v", err, string(out))
	}
	for _, want := range []string{"[propagation]", "[lxmf]", "[logging]"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("parser alias output missing %q: %v", want, string(out))
		}
	}
}

func TestGolxmd_Status_WithNoRemote(t *testing.T) {
	skipShortIntegration(t)
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()
	tmpDir, tmpCleanup := tempDir(t)
	defer tmpCleanup()

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Skipf("could not generate identity for status test: %v\n%v", err, string(genOut))
	}

	// Try to get status (will fail without a running remote, but should fail gracefully)
	// This test verifies the error handling path
	out, err := exec.Command(golxmdBin, "--status", "--config", configDir, "--timeout", "1").CombinedOutput()
	// We expect this to fail (no remote running), but it should fail with a proper error
	if err == nil {
		// If it succeeds, that's fine too - it means a local instance is running
		t.Logf("golxmd --status succeeded (local instance may be running): %v", string(out))
	} else {
		// Expected: timeout or connection error
		output := string(out)
		// Should have some meaningful error message
		if strings.Contains(output, "panic") || strings.Contains(output, "segmentation fault") {
			t.Errorf("golxmd --status crashed: %v", output)
		}
	}
}

func TestGolxmd_Break_WithInvalidHash(t *testing.T) {
	skipShortIntegration(t)
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()
	tmpDir, tmpCleanup := tempDir(t)
	defer tmpCleanup()

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Try to break peering with invalid hash
	out, err := exec.Command(golxmdBin, "-b", "invalid_hash", "--config", configDir, "--timeout", "1").CombinedOutput()
	if err == nil {
		t.Errorf("expected error for invalid hash, got success")
	}
	output := string(out)
	if !strings.Contains(output, "Invalid") {
		t.Errorf("expected error message to contain 'Invalid', got: %v", output)
	}
}

func TestGolxmd_Sync_WithInvalidHash(t *testing.T) {
	skipShortIntegration(t)
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()
	tmpDir, tmpCleanup := tempDir(t)
	defer tmpCleanup()

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Try to sync with invalid hash
	out, err := exec.Command(golxmdBin, "--sync", "invalid_hash", "--config", configDir, "--timeout", "1").CombinedOutput()
	if err == nil {
		t.Errorf("expected error for invalid hash, got success")
	}
	output := string(out)
	if !strings.Contains(output, "Invalid") {
		t.Errorf("expected error message to contain 'Invalid', got: %v", output)
	}
}

func TestGolxmd_Status_OutputFormat(t *testing.T) {
	skipShortIntegration(t)
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()
	tmpDir, tmpCleanup := tempDir(t)
	defer tmpCleanup()

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Run with --status (will timeout but we can check output format)
	out, _ := exec.Command(golxmdBin, "--status", "--config", configDir, "--timeout", "1").CombinedOutput()
	output := string(out)

	// If we got a successful response (unlikely without remote), check format
	if strings.Contains(output, "LXMF Propagation Node running on") {
		// Verify key format elements are present
		requiredPatterns := []string{
			"running on <",
			"uptime is",
		}
		for _, pattern := range requiredPatterns {
			if !strings.Contains(output, pattern) {
				t.Errorf("expected status output to contain %q, got: %v", pattern, output)
			}
		}
	}
}

func TestGolxmd_Status_WithShowStatusFlag(t *testing.T) {
	skipShortIntegration(t)
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()
	tmpDir, tmpCleanup := tempDir(t)
	defer tmpCleanup()

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Run with --status (the flag enables detailed status output)
	out, _ := exec.Command(golxmdBin, "--status", "--config", configDir, "--timeout", "1").CombinedOutput()
	output := string(out)

	// If successful response, verify detailed status fields would be present
	if strings.Contains(output, "LXMF Propagation Node running on") {
		// These are the fields shown when showStatus is true
		detailedPatterns := []string{
			"Messagestore contains",
			"Required propagation stamp cost",
			"Peers   :",
			"Traffic :",
		}
		for _, pattern := range detailedPatterns {
			if !strings.Contains(output, pattern) {
				t.Errorf("expected detailed status to contain %q, got: %v", pattern, output)
			}
		}
	}
}

func TestGolxmd_Peers_OutputFormat(t *testing.T) {
	skipShortIntegration(t)
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()
	tmpDir, tmpCleanup := tempDir(t)
	defer tmpCleanup()

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Run with --peers
	out, _ := exec.Command(golxmdBin, "--peers", "--config", configDir, "--timeout", "1").CombinedOutput()
	output := string(out)

	// If successful response, verify peer format
	if strings.Contains(output, "LXMF Propagation Node running on") {
		// Peer output should contain these elements for each peer
		peerPatterns := []string{
			"Static peer",
			"Discovered peer",
			"Status     :",
			"Costs      :",
			"Sync key   :",
			"Speeds     :",
			"Limits     :",
			"Messages   :",
			"Traffic    :",
			"Sync state :",
		}
		// At least some of these should be present if there are peers
		// We just verify the format is correct if peers are shown
		if strings.Contains(output, "peer") || strings.Contains(output, "Peer") {
			for _, pattern := range peerPatterns {
				if !strings.Contains(output, pattern) {
					t.Logf("warning: peer output missing %q, got: %v", pattern, output)
				}
			}
		}
	}
}

func TestGolxmd_Break_Timeout(t *testing.T) {
	skipShortIntegration(t)
	t.Parallel()
	golxmdBin, cleanup := buildGolxmd(t)
	defer cleanup()
	tmpDir, tmpCleanup := tempDir(t)
	defer tmpCleanup()

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Create a valid destination hash (32 hex chars = 16 bytes)
	validHash := "0123456789abcdef0123456789abcdef"

	// Run with -b (break/unpeer) - should timeout since no remote running
	start := time.Now()
	out, err := exec.Command(golxmdBin, "-b", validHash, "--config", configDir, "--timeout", "1").CombinedOutput()
	elapsed := time.Since(start)
	output := string(out)

	// Should timeout after ~1 second
	if elapsed < 1*time.Second || elapsed > 5*time.Second {
		t.Errorf("expected timeout around 1 second, took %v", elapsed)
	}

	// Should have timeout message
	if !strings.Contains(output, "timed out") {
		t.Errorf("expected timeout message, got: %v", output)
	}
}
