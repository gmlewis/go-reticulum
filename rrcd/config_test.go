// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestDefaultHubConfig(t *testing.T) {
	t.Parallel()
	c := DefaultHubConfig()
	// Every default captured from Python HubRuntimeConfig (config.py:13-47).
	if c.DestName != "rrc.hub" {
		t.Errorf("DestName = %v", c.DestName)
	}
	if !c.AnnounceOnStart || c.AnnouncePeriodS != 0.0 {
		t.Errorf("announce defaults = %v, %v", c.AnnounceOnStart, c.AnnouncePeriodS)
	}
	if c.HubName != "rrc" {
		t.Errorf("HubName = %v", c.HubName)
	}
	if c.Greeting != nil {
		t.Errorf("Greeting = %v, want nil", c.Greeting)
	}
	if len(c.TrustedIdentities) != 0 || len(c.BannedIdentities) != 0 {
		t.Errorf("identity lists must start empty")
	}
	if c.RoomRegistryPruneAfterS != 2592000.0 {
		t.Errorf("RoomRegistryPruneAfterS = %v", c.RoomRegistryPruneAfterS)
	}
	if c.RoomRegistryPruneIntervalS != 3600.0 {
		t.Errorf("RoomRegistryPruneIntervalS = %v", c.RoomRegistryPruneIntervalS)
	}
	if c.RoomInviteTimeoutS != 900.0 {
		t.Errorf("RoomInviteTimeoutS = %v", c.RoomInviteTimeoutS)
	}
	if c.IncludeJoinedMemberList {
		t.Errorf("IncludeJoinedMemberList must default false")
	}
	if c.MaxNickBytes != 32 || c.MaxRoomsPerSession != 32 ||
		c.MaxRoomNameBytes != 64 || c.MaxMsgBodyBytes != 350 ||
		c.RateLimitMsgsPerMinute != 240 {
		t.Errorf("limit defaults wrong: %+v", c)
	}
	if c.PingIntervalS != 0.0 || c.PingTimeoutS != 0.0 {
		t.Errorf("ping defaults wrong")
	}
	if c.MaxResourceBytes != 262144 || c.MaxPendingResourceExpectations != 8 ||
		c.ResourceExpectationTTLs != 30.0 || !c.EnableResourceTransfer {
		t.Errorf("resource defaults wrong")
	}
	if c.LogLevel != "INFO" || c.LogRNSLevel != "WARNING" || !c.LogConsole {
		t.Errorf("log defaults wrong")
	}
	if c.LogFile != nil || c.LogDatefmt != nil {
		t.Errorf("optional log paths must default nil")
	}
	if c.LogFormat != "%(asctime)s %(levelname)s %(name)s[%(threadName)s]: %(message)s" {
		t.Errorf("LogFormat = %v", c.LogFormat)
	}
	if c.ConfigPath != nil || c.RoomRegistryPath != nil || c.Configdir != nil ||
		c.IdentityPath != nil {
		t.Errorf("path fields must default nil")
	}
}

// TestApplyConfigData covers the flatten/remap/allowed-list behavior
// including both Python test cases from tests/test_config.py.
func TestApplyConfigData(t *testing.T) {
	t.Parallel()
	base := DefaultHubConfig()

	// The two Python test cases.
	t.Run("dest_name_override_ignored", func(t *testing.T) {
		t.Parallel()
		updated := ApplyConfigData(base, map[string]any{
			"dest_name": "custom.hub",
			"hub":       map[string]any{"dest_name": "custom.hub"},
			"hub_name":  "custom-name",
		})
		if updated.DestName != "rrc.hub" {
			t.Errorf("DestName = %v, want rrc.hub", updated.DestName)
		}
		if updated.HubName != "custom-name" {
			t.Errorf("HubName = %v, want custom-name", updated.HubName)
		}
	})

	t.Run("hub_table_flattens_over_top_level", func(t *testing.T) {
		t.Parallel()
		updated := ApplyConfigData(base, map[string]any{
			"hub_name": "top-level",
			"hub":      map[string]any{"hub_name": "hub-level"},
		})
		if updated.HubName != "hub-level" {
			t.Errorf("HubName = %v, want hub-level", updated.HubName)
		}
	})

	t.Run("logging_table_remapped", func(t *testing.T) {
		t.Parallel()
		updated := ApplyConfigData(base, map[string]any{
			"logging": map[string]any{
				"level":     "DEBUG",
				"rns_level": "ERROR",
				"console":   false,
				"file":      "/tmp/x.log",
				"format":    "custom fmt",
				"datefmt":   "%H:%M",
			},
		})
		if updated.LogLevel != "DEBUG" || updated.LogRNSLevel != "ERROR" ||
			updated.LogConsole || updated.LogFile == nil || *updated.LogFile != "/tmp/x.log" ||
			updated.LogFormat != "custom fmt" || updated.LogDatefmt == nil {
			t.Fatalf("logging remap wrong: %+v", updated)
		}
	})

	t.Run("identity_lists_only_when_list", func(t *testing.T) {
		t.Parallel()
		updated := ApplyConfigData(base, map[string]any{
			"trusted_identities": []any{"aa", 1},
			"banned_identities":  "not-a-list",
		})
		if !reflect.DeepEqual(updated.TrustedIdentities, []string{"aa", "1"}) {
			t.Errorf("TrustedIdentities = %v", updated.TrustedIdentities)
		}
		if updated.BannedIdentities != nil {
			t.Errorf("BannedIdentities = %v, want empty (non-list ignored)", updated.BannedIdentities)
		}
	})

	t.Run("legacy_announce_bool", func(t *testing.T) {
		t.Parallel()
		updated := ApplyConfigData(base, map[string]any{"announce": false})
		if updated.AnnounceOnStart {
			t.Errorf("legacy announce=false ignored")
		}
		// announce_on_start takes precedence when present in the same data.
		updated = ApplyConfigData(base, map[string]any{
			"announce":          false,
			"announce_on_start": true,
		})
		if !updated.AnnounceOnStart {
			t.Errorf("announce_on_start precedence broken")
		}
	})

	t.Run("empty_strings_become_nil", func(t *testing.T) {
		t.Parallel()
		updated := ApplyConfigData(base, map[string]any{
			"configdir": "", "greeting": "", "log_file": "", "log_datefmt": "",
		})
		if updated.Configdir != nil || updated.Greeting != nil ||
			updated.LogFile != nil || updated.LogDatefmt != nil {
			t.Errorf("empty strings must map to nil: %+v", updated)
		}
	})

	t.Run("no_type_coercion", func(t *testing.T) {
		t.Parallel()
		// A string value for a bool field is not applied (Go keeps declared
		// types); an int field with a string is likewise skipped.
		updated := ApplyConfigData(base, map[string]any{
			"announce_on_start":  "yes",
			"max_nick_bytes":     "not-int",
			"max_msg_body_bytes": int64(400),
		})
		if updated.AnnounceOnStart != base.AnnounceOnStart {
			t.Errorf("string for bool field must not coerce")
		}
		if updated.MaxNickBytes != base.MaxNickBytes {
			t.Errorf("string for int field must not coerce")
		}
		if updated.MaxMsgBodyBytes != 400 {
			t.Errorf("int64 int field not applied: %v", updated.MaxMsgBodyBytes)
		}
	})

	t.Run("unknown_keys_ignored", func(t *testing.T) {
		t.Parallel()
		updated := ApplyConfigData(base, map[string]any{"bogus_key": 1})
		if !reflect.DeepEqual(updated, base) {
			t.Errorf("unknown key changed the config")
		}
	})

	t.Run("nil_data_returns_base", func(t *testing.T) {
		t.Parallel()
		if !reflect.DeepEqual(ApplyConfigData(base, nil), base) {
			t.Errorf("nil data changed the config")
		}
	})
}

func TestFormatReloadValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   any
		want string
	}{
		{nil, "(none)"},
		{(*string)(nil), "(none)"},
		{true, "True"},
		{false, "False"},
		{42, "42"},
		{int64(7), "7"},
		{0.0, "0.0"},
		{900.0, "900.0"},
		{1e16, "1e+16"},
		{[]string{"a", "b"}, "len=2"},
		{[]any{}, "len=0"},
		{"plain", "plain"},
		{"spaced   out\ttext", "spaced out text"},
		{strings.Repeat("x", 81), strings.Repeat("x", 77) + "..."},
		{strings.Repeat("x", 80), strings.Repeat("x", 80)},
	} {
		if got := FormatReloadValue(tc.in); got != tc.want {
			t.Errorf("FormatReloadValue(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDiffConfigSummary(t *testing.T) {
	t.Parallel()
	oldCfg := DefaultHubConfig()
	newCfg := DefaultHubConfig()
	newCfg.HubName = "other"
	newCfg.MaxNickBytes = 64
	newCfg.Greeting = new("hello")
	newCfg.TrustedIdentities = []string{"aa"}
	got := DiffConfigSummary(oldCfg, newCfg)
	sorted := []string{
		"greeting: (none) -> hello",
		"hub_name: rrc -> other",
		"max_nick_bytes: 32 -> 64",
		"trusted_identities: len=0 -> len=1",
	}
	if len(got) != len(sorted) {
		t.Fatalf("diff lines = %v, want %v", got, sorted)
	}
	for i, line := range got {
		if line != sorted[i] {
			t.Errorf("diff line %v = %q, want %q", i, line, sorted[i])
		}
	}
	// config_path is excluded from diffs.
	a := oldCfg
	b := oldCfg
	a.ConfigPath = new("/old")
	b.ConfigPath = new("/new")
	if lines := DiffConfigSummary(a, b); len(lines) != 0 {
		t.Errorf("config_path must be excluded; got %v", lines)
	}
}

func TestDefaultRrcdDir(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel.
	t.Setenv("RRCD_HOME", "/opt/rrcd-home")
	if got, want := DefaultRrcdDir(), "/opt/rrcd-home"; got != want {
		t.Errorf("DefaultRrcdDir = %q, want %q", got, want)
	}
	if got, want := DefaultConfigPath(), "/opt/rrcd-home/rrcd.toml"; got != want {
		t.Errorf("DefaultConfigPath = %q, want %q", got, want)
	}
	if got, want := DefaultIdentityPath(), "/opt/rrcd-home/hub_identity"; got != want {
		t.Errorf("DefaultIdentityPath = %q, want %q", got, want)
	}
	if got, want := DefaultRoomRegistryPath(), "/opt/rrcd-home/rooms.toml"; got != want {
		t.Errorf("DefaultRoomRegistryPath = %q, want %q", got, want)
	}

	// Empty-string override falls through to home.
	t.Setenv("RRCD_HOME", "")
	home := testutils.TempDir(t, "rrcd-home-")
	t.Setenv("HOME", home)
	if got, want := DefaultRrcdDir(), home+"/.rrcd"; got != want {
		t.Errorf("DefaultRrcdDir with empty override = %q, want %q", got, want)
	}
}

func TestEnsurePrivateDir(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(testutils.TempDir(t, "rrcd-priv-"), "nested", "deep")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatalf("EnsurePrivateDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("dir missing after EnsurePrivateDir: %v", err)
	}
}
