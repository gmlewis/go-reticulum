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
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestResolveConfigDir(t *testing.T) {
	t.Run("explicit config dir", func(t *testing.T) {
		tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
		defer cleanup()
		home := filepath.Join(tmpDir, "home")
		explicit := filepath.Join(tmpDir, "explicit")
		got := resolveConfigDirCustom(explicit, home, "/etc/lxmd")
		if got != explicit {
			t.Errorf("got %v, want %v", got, explicit)
		}
	})

	t.Run("etc config dir exists", func(t *testing.T) {
		tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
		defer cleanup()
		home := filepath.Join(tmpDir, "home")
		etc := filepath.Join(tmpDir, "etc")
		mustTest(t, os.MkdirAll(etc, 0o755))
		mustTest(t, os.WriteFile(filepath.Join(etc, "config"), []byte(""), 0o644))

		got := resolveConfigDirCustom("", home, etc)
		if got != etc {
			t.Errorf("got %v, want %v", got, etc)
		}
	})

	t.Run("user config dir exists", func(t *testing.T) {
		tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
		defer cleanup()
		home := filepath.Join(tmpDir, "home")
		userConfig := filepath.Join(home, ".config", "lxmd")
		mustTest(t, os.MkdirAll(userConfig, 0o755))
		mustTest(t, os.WriteFile(filepath.Join(userConfig, "config"), []byte(""), 0o644))

		got := resolveConfigDirCustom("", home, "/nonexistent/etc")
		if got != userConfig {
			t.Errorf("got %v, want %v", got, userConfig)
		}
	})

	t.Run("fallback to dot lxmd", func(t *testing.T) {
		tmpDir, cleanup := testutils.TempDir(t, tempDirPrefix)
		defer cleanup()
		home := filepath.Join(tmpDir, "home")
		dotLxmd := filepath.Join(home, ".lxmd")
		got := resolveConfigDirCustom("", home, "/nonexistent/etc")
		if got != dotLxmd {
			t.Errorf("got %v, want %v", got, dotLxmd)
		}
	})
}

func TestApplyConfig(t *testing.T) {
	c := &clientT{}
	t.Run("defaults", func(t *testing.T) {
		cfg := make(map[string]map[string]string)
		got, err := c.applyConfig(cfg)
		mustTest(t, err)

		if got.DisplayName != "Anonymous Peer" {
			t.Errorf("DisplayName: got %q, want %q", got.DisplayName, "Anonymous Peer")
		}
		if got.EnablePropagationNode != false {
			t.Errorf("EnablePropagationNode: got %v, want %v", got.EnablePropagationNode, false)
		}
		if got.Autopeer != true {
			t.Errorf("Autopeer: got %v, want %v", got.Autopeer, true)
		}
		if got.PropagationStampCostTarget != 16 {
			t.Errorf("PropagationStampCostTarget: got %v, want 16", got.PropagationStampCostTarget)
		}
		if got.LogLevel != -1 {
			t.Errorf("LogLevel: got %v, want -1", got.LogLevel)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		cfg := map[string]map[string]string{
			"lxmf": {
				"display_name":                        "My Peer",
				"announce_at_start":                   "yes",
				"announce_interval":                   "120",
				"delivery_transfer_max_accepted_size": "2.5",
			},
			"propagation": {
				"enable_node":                   "yes",
				"node_name":                     "My Node",
				"autopeer":                      "no",
				"max_peers":                     "50",
				"propagation_stamp_cost_target": "20",
				"static_peers":                  "e17f833c, 5a2d0029",
			},
			"logging": {
				"loglevel": "5",
			},
		}
		got, err := c.applyConfig(cfg)
		mustTest(t, err)

		if got.DisplayName != "My Peer" {
			t.Errorf("DisplayName: got %q, want %q", got.DisplayName, "My Peer")
		}
		if got.PeerAnnounceAtStart != true {
			t.Errorf("PeerAnnounceAtStart: got %v, want true", got.PeerAnnounceAtStart)
		}
		if got.PeerAnnounceInterval == nil || *got.PeerAnnounceInterval != 120*60 {
			t.Errorf("PeerAnnounceInterval: got %v, want %v", got.PeerAnnounceInterval, 120*60)
		}
		if got.DeliveryTransferMaxAcceptedSize != 2.5 {
			t.Errorf("DeliveryTransferMaxAcceptedSize: got %v, want 2.5", got.DeliveryTransferMaxAcceptedSize)
		}
		if got.EnablePropagationNode != true {
			t.Errorf("EnablePropagationNode: got %v, want true", got.EnablePropagationNode)
		}
		if got.NodeName != "My Node" {
			t.Errorf("NodeName: got %v, want My Node", got.NodeName)
		}
		if got.Autopeer != false {
			t.Errorf("Autopeer: got %v, want false", got.Autopeer)
		}
		if got.MaxPeers == nil || *got.MaxPeers != 50 {
			t.Errorf("MaxPeers: got %v, want 50", got.MaxPeers)
		}
		if got.PropagationStampCostTarget != 20 {
			t.Errorf("PropagationStampCostTarget: got %v, want 20", got.PropagationStampCostTarget)
		}
		if len(got.StaticPeers) != 2 {
			t.Errorf("StaticPeers: got %v, want 2", len(got.StaticPeers))
		}
		if got.LogLevel != 5 {
			t.Errorf("LogLevel: got %v, want 5", got.LogLevel)
		}
	})

	t.Run("clamping", func(t *testing.T) {
		cfg := map[string]map[string]string{
			"lxmf": {
				"delivery_transfer_max_accepted_size": "0.1",
			},
			"propagation": {
				"message_storage_limit":                  "0.001",
				"propagation_transfer_max_accepted_size": "0.1",
				"propagation_sync_max_accepted_size":     "0.1",
				"propagation_stamp_cost_target":          "5",
				"propagation_stamp_cost_flexibility":     "-5",
				"peering_cost":                           "-5",
				"remote_peering_cost_max":                "-5",
			},
		}
		got, err := c.applyConfig(cfg)
		mustTest(t, err)
		if got.DeliveryTransferMaxAcceptedSize != 0.38 {
			t.Errorf("DeliveryTransferMaxAcceptedSize: got %v, want 0.38", got.DeliveryTransferMaxAcceptedSize)
		}
		if got.MessageStorageLimit != 0.005 {
			t.Errorf("MessageStorageLimit: got %v, want 0.005", got.MessageStorageLimit)
		}
		if got.PropagationTransferMaxAcceptedSize != 0.38 {
			t.Errorf("PropagationTransferMaxAcceptedSize: got %v, want 0.38", got.PropagationTransferMaxAcceptedSize)
		}
		if got.PropagationSyncMaxAcceptedSize != 0.38 {
			t.Errorf("PropagationSyncMaxAcceptedSize: got %v, want 0.38", got.PropagationSyncMaxAcceptedSize)
		}
		if got.PropagationStampCostTarget != 13 {
			t.Errorf("PropagationStampCostTarget: got %v, want 13", got.PropagationStampCostTarget)
		}
		if got.PropagationStampCostFlexibility != 0 {
			t.Errorf("PropagationStampCostFlexibility: got %v, want 0", got.PropagationStampCostFlexibility)
		}
		if got.PeeringCost != 0 {
			t.Errorf("PeeringCost: got %v, want 0", got.PeeringCost)
		}
		if got.RemotePeeringCostMax != 0 {
			t.Errorf("RemotePeeringCostMax: got %v, want 0", got.RemotePeeringCostMax)
		}
	})
}

func TestLoadHashList(t *testing.T) {
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	path := filepath.Join(td, "hashes")

	valid1 := "0123456789abcdef0123456789abcdef"
	valid2 := "fedcba9876543210fedcba9876543210"
	invalid1 := "too_short"
	invalid2 := "not_hex_0123456789abcdef01234567"
	content := valid1 + "\n" + invalid1 + "\n" + valid2 + "\n" + invalid2 + "\n"
	mustTest(t, os.WriteFile(path, []byte(content), 0o644))

	got := loadHashList(path)
	if len(got) != 2 {
		t.Fatalf("got %v hashes, want 2", len(got))
	}

	if len(loadHashList("/nonexistent")) != 0 {
		t.Errorf("expected missing file to return zero hashes")
	}
}

func TestEnsureConfig(t *testing.T) {
	td, cleanup := testutils.TempDir(t, tempDirPrefix)
	defer cleanup()
	configDir := filepath.Join(td, "lxmd")
	configPath := filepath.Join(configDir, "config")

	mustTest(t, ensureConfig(configDir))

	if !isFile(configPath) {
		t.Errorf("config file %q not created", configPath)
	}

	data, err := os.ReadFile(configPath)
	mustTest(t, err)
	if !strings.Contains(string(data), "[propagation]") {
		t.Errorf("config file doesn't contain [propagation]")
	}
}

func TestParseIntWarning(t *testing.T) {
	t.Parallel()

	var capturedLog string
	logger := rns.NewLogger()
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(s string) { capturedLog += s })
	logger.SetLogLevel(rns.LogInfo)

	c := &clientT{logger: logger}
	if got := c.parseInt("not-a-number"); got != 0 {
		t.Errorf("parseInt(\"not-a-number\") = %v, want 0", got)
	}
	if !strings.Contains(capturedLog, "Invalid integer value") {
		t.Errorf("expected warning log, got %q", capturedLog)
	}
}

func TestParseFloatWarning(t *testing.T) {
	t.Parallel()

	var capturedLog string
	logger := rns.NewLogger()
	logger.SetLogDest(rns.LogCallback)
	logger.SetLogCallback(func(s string) { capturedLog += s })
	logger.SetLogLevel(rns.LogInfo)

	c := &clientT{logger: logger}
	if got := c.parseFloat("not-a-float"); got != 0 {
		t.Errorf("parseFloat(\"not-a-float\") = %v, want 0", got)
	}
	if !strings.Contains(capturedLog, "Invalid float value") {
		t.Errorf("expected warning log, got %q", capturedLog)
	}
}

func TestParseINI(t *testing.T) {
	input := `# Comment
key1 = value1
  key2 = value2  # inline comment

[section2]
key3 = value3
`
	got := parseINI(input)
	want := map[string]map[string]string{
		"section2": {
			"key3": "value3",
		},
	}

	if len(got) != len(want) {
		t.Fatalf("got %v sections, want %v", len(got), len(want))
	}

	for s, keys := range want {
		if len(got[s]) != len(keys) {
			t.Errorf("section %v: got %v keys, want %v", s, len(got[s]), len(keys))
		}
		for k, v := range keys {
			if got[s][k] != v {
				t.Errorf("section %v, key %v: got %q, want %q", s, k, got[s][k], v)
			}
		}
	}
}
