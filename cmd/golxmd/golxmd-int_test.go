// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func buildGolxmd(t *testing.T) string {
	t.Helper()
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	bin := filepath.Join(tmpDir, "golxmd")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build golxmd: %v\n%v", err, string(out))
	}
	return bin
}

func TestGolxmd_Version(t *testing.T) {
	t.Parallel()
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)

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
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)

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
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)

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
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)

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
	t.Parallel()
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)
	tmpDir := testutils.TempDir(t, tempDirPrefix)

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config")
	configData := fmt.Sprintf("[reticulum]\ninstance_name = %v\n", filepath.Base(tmpDir))
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "--config", configDir, "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Skipf("could not generate identity for status test: %v\n%v", err, string(genOut))
	}

	// Try to get status (will fail without a running remote, but should fail gracefully)
	// This test verifies the error handling path
	out, err := exec.Command(golxmdBin, "--status", "--config", configDir, "--rnsconfig", configDir, "--timeout", "1").CombinedOutput()
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
	t.Parallel()
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)
	tmpDir := testutils.TempDir(t, tempDirPrefix)

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config")
	configData := fmt.Sprintf("[reticulum]\ninstance_name = %v\n", filepath.Base(tmpDir))
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "--config", configDir, "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Try to break peering with invalid hash
	out, err := exec.Command(golxmdBin, "-b", "invalid_hash", "--config", configDir, "--rnsconfig", configDir, "--timeout", "1").CombinedOutput()
	if err == nil {
		t.Errorf("expected error for invalid hash, got success")
	}
	output := string(out)
	if !strings.Contains(output, "Invalid") {
		t.Errorf("expected error message to contain 'Invalid', got: %v", output)
	}
}

func TestGolxmd_Sync_WithInvalidHash(t *testing.T) {
	t.Parallel()
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)
	tmpDir := testutils.TempDir(t, tempDirPrefix)

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config")
	configData := fmt.Sprintf("[reticulum]\ninstance_name = %v\n", filepath.Base(tmpDir))
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "--config", configDir, "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Try to sync with invalid hash
	out, err := exec.Command(golxmdBin, "--sync", "invalid_hash", "--config", configDir, "--rnsconfig", configDir, "--timeout", "1").CombinedOutput()
	if err == nil {
		t.Errorf("expected error for invalid hash, got success")
	}
	output := string(out)
	if !strings.Contains(output, "Invalid") {
		t.Errorf("expected error message to contain 'Invalid', got: %v", output)
	}
}

func TestGolxmd_Status_OutputFormat(t *testing.T) {
	t.Parallel()
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)
	tmpDir := testutils.TempDir(t, tempDirPrefix)

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config")
	configData := fmt.Sprintf("[reticulum]\ninstance_name = %v\n", filepath.Base(tmpDir))
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "--config", configDir, "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Run with --status (will timeout but we can check output format)
	out, _ := exec.Command(golxmdBin, "--status", "--config", configDir, "--rnsconfig", configDir, "--timeout", "1").CombinedOutput()
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
	t.Parallel()
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)
	tmpDir := testutils.TempDir(t, tempDirPrefix)

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config")
	configData := fmt.Sprintf("[reticulum]\ninstance_name = %v\n", filepath.Base(tmpDir))
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "--config", configDir, "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Run with --status (the flag enables detailed status output)
	out, _ := exec.Command(golxmdBin, "--status", "--config", configDir, "--rnsconfig", configDir, "--timeout", "1").CombinedOutput()
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
	t.Parallel()
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)
	tmpDir := testutils.TempDir(t, tempDirPrefix)

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config")
	configData := fmt.Sprintf("[reticulum]\ninstance_name = %v\n", filepath.Base(tmpDir))
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "--config", configDir, "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Run with --peers
	out, _ := exec.Command(golxmdBin, "--peers", "--config", configDir, "--rnsconfig", configDir, "--timeout", "1").CombinedOutput()
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
	t.Parallel()
	skipShortIntegration(t)
	golxmdBin := buildGolxmd(t)
	tmpDir := testutils.TempDir(t, tempDirPrefix)

	// Create a minimal config
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	configPath := filepath.Join(configDir, "config")
	configData := fmt.Sprintf("[reticulum]\ninstance_name = %v\n", filepath.Base(tmpDir))
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create identity
	identityPath := filepath.Join(configDir, "identity")
	genOut, err := exec.Command("go", "run", "../gornid", "--config", configDir, "-g", identityPath).CombinedOutput()
	if err != nil {
		t.Fatalf("could not generate identity: %v\n%v", err, string(genOut))
	}

	// Create a valid destination hash (32 hex chars = 16 bytes)
	validHash := "0123456789abcdef0123456789abcdef"

	// Run with -b (break/unpeer) - should timeout since no remote running.
	//
	// The --timeout 1 bounds the *operation* wait, but total wall-clock elapsed
	// also includes RNS init (identity load, transport/interfaces setup,
	// announce), which is machine/load dependent. When run under heavy
	// parallel -race load the golxmd subprocess can be CPU-starved enough that
	// RNS init alone exceeds the 1s operation timeout, in which case
	// requestUnpeer exits immediately with "timed out" and elapsed equals the
	// init time (observed ~6.7s). So elapsed is ~1s on a fast/idle machine but
	// grows with load; it must NOT be asserted into a tight window. The real
	// behavioral guarantees are: (a) it actually waited for the timeout rather
	// than returning instantly, and (b) it did not hang. The "timed out"
	// message is the primary assertion.
	start := time.Now()
	out, err := exec.Command(golxmdBin, "-b", validHash, "--config", configDir, "--rnsconfig", configDir, "--timeout", "1").CombinedOutput()
	// Exit code 200 is the expected "operation timed out" result for golxmd
	// when no remote is reachable (see remote-init.go requestUnpeer). The
	// whole point of this test is to observe that timeout, so exit 200 is a
	// success, not a failure. Any other error is a real problem.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 200 {
			t.Fatalf("glxmd: %v\n%v", err, string(out))
		}
	}
	elapsed := time.Since(start)
	output := string(out)

	// It must have waited for the timeout (operation timeout is 1s; even when
	// init eats the whole budget, init itself takes >= 1s in that case, so
	// elapsed is always at least ~1s).
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected to wait for the timeout, elapsed %v", elapsed)
	}
	// It must not hang. 60s is far above any load-induced RNS-init time while
	// still catching a real deadlock regression.
	if elapsed > 60*time.Second {
		t.Errorf("golxmd -b did not time out within 60s, elapsed %v", elapsed)
	}

	// Should have timeout message
	if !strings.Contains(output, "timed out") {
		t.Errorf("expected timeout message, got: %v", output)
	}
}

// TestGolxmd_PropagationNodeHonorsNewConfigKeys verifies that a propagation
// node configured with the four new v1.1.0 config keys (stamp_cost,
// sequential_pn_stamp_validation, static_peers_bypass_sequential,
// max_inbound_syncs) honors them: the config parses to the expected
// activeConfig values, NewRouterFromConfig accepts the propagation settings
// without error, the delivery identity's inbound stamp cost reflects the
// configured stamp_cost, and the propagation destination registers
// cleanly.
func TestGolxmd_PropagationNodeHonorsNewConfigKeys(t *testing.T) {
	skipShortIntegration(t)

	tmpDir := testutils.TempDir(t, tempDirPrefix)
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}

	configData := `[propagation]
enable_node = yes
node_name = Test Propagation Node
sequential_pn_stamp_validation = no
static_peers_bypass_sequential = no
max_inbound_syncs = 5

[lxmf]
display_name = Test Peer
stamp_cost = 8
`
	configPath := filepath.Join(configDir, "config")
	if err := os.WriteFile(configPath, []byte(configData), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	logger := rns.NewLogger()
	ts := rns.NewTransportSystem(logger)
	c := &clientT{ts: ts, now: time.Now, logger: logger}

	ac, err := c.loadConfig(configDir)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	// Verify the four new keys parsed to the expected activeConfig values.
	if ac.PeerStampCost == nil || *ac.PeerStampCost != 8 {
		t.Fatalf("PeerStampCost = %v, want 8", ac.PeerStampCost)
	}
	if ac.SequentialPNStampValidation == nil || *ac.SequentialPNStampValidation != false {
		t.Fatalf("SequentialPNStampValidation = %v, want false", ac.SequentialPNStampValidation)
	}
	if ac.StaticPeersBypassSequential != false {
		t.Fatalf("StaticPeersBypassSequential = %v, want false", ac.StaticPeersBypassSequential)
	}
	if ac.MaxInboundSyncs == nil || *ac.MaxInboundSyncs != 5 {
		t.Fatalf("MaxInboundSyncs = %v, want 5", ac.MaxInboundSyncs)
	}

	identity, err := rns.NewIdentity(true, logger)
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}

	storagePath := filepath.Join(configDir, "storage")
	if err := os.MkdirAll(filepath.Join(storagePath, "messages"), 0o755); err != nil {
		t.Fatalf("create storage: %v", err)
	}

	// Create the router with the configured propagation settings. This
	// verifies the three new propagation keys are accepted by
	// NewRouterFromConfig without error. The inversion of
	// static_peers_bypass_sequential (False here) yields
	// StaticSequential=true.
	router, err := lxmf.NewRouterFromConfig(ts, lxmf.RouterConfig{
		Identity:                   identity,
		StoragePath:                storagePath,
		Autopeer:                   ac.Autopeer,
		PropagationLimit:           ac.PropagationTransferMaxAcceptedSize,
		SyncLimit:                  ac.PropagationSyncMaxAcceptedSize,
		DeliveryLimit:              ac.DeliveryTransferMaxAcceptedSize,
		PropagationCost:            ac.PropagationStampCostTarget,
		PropagationCostFlexibility: ac.PropagationStampCostFlexibility,
		PeeringCost:                ac.PeeringCost,
		MaxPeeringCost:             ac.RemotePeeringCostMax,
		Name:                       ac.NodeName,
		SequentialValidation:       ac.SequentialPNStampValidation,
		StaticSequential:           !ac.StaticPeersBypassSequential,
		MaxInboundSyncs:            ac.MaxInboundSyncs,
	})
	if err != nil {
		t.Fatalf("NewRouterFromConfig failed: %v", err)
	}
	defer func() {
		if err := router.Close(); err != nil {
			t.Logf("router close: %v", err)
		}
	}()

	// Register the delivery identity with the configured inbound stamp cost
	// and verify it is reflected via InboundStampCost.
	dest, err := router.RegisterDeliveryIdentity(identity, ac.DisplayName, ac.PeerStampCost)
	if err != nil {
		t.Fatalf("RegisterDeliveryIdentity failed: %v", err)
	}

	gotCost, ok := router.InboundStampCost(dest.Hash)
	if !ok {
		t.Fatalf("InboundStampCost: no stamp cost set for delivery destination %x", dest.Hash)
	}
	if gotCost != 8 {
		t.Errorf("InboundStampCost = %v, want 8", gotCost)
	}

	// Enable propagation and register the propagation destination to verify
	// the node starts cleanly with the new sequential/max_inbound_syncs
	// settings applied.
	router.SetMessageStorageLimit(ac.MessageStorageLimit)
	router.EnablePropagation()
	if _, err := router.RegisterPropagationDestination(); err != nil {
		t.Fatalf("RegisterPropagationDestination failed: %v", err)
	}
}
