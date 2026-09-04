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

	t.Run("identity_lists_accept_python_semantics", func(t *testing.T) {
		t.Parallel()
		// Python keeps a non-list value in the field and both use sites
		// iterate it: a string character-splits (so every entry fails
		// identity-hash parsing), a list converts with str() per element,
		// and any other scalar renders as its str() form (also failing).
		updated := ApplyConfigData(base, map[string]any{
			"trusted_identities": []any{"aa", 1},
			"banned_identities":  "not-a-list",
		})
		if !reflect.DeepEqual(updated.TrustedIdentities, []string{"aa", "1"}) {
			t.Errorf("TrustedIdentities = %v", updated.TrustedIdentities)
		}
		if !reflect.DeepEqual(updated.BannedIdentities,
			[]string{"n", "o", "t", "-", "a", "-", "l", "i", "s", "t"}) {
			t.Errorf("BannedIdentities = %v, want the Python character split", updated.BannedIdentities)
		}
		// A non-list scalar rides through as its str() rendering.
		updated = ApplyConfigData(base, map[string]any{"trusted_identities": 5})
		if !reflect.DeepEqual(updated.TrustedIdentities, []string{"5"}) {
			t.Errorf("TrustedIdentities for int = %v, want [5]", updated.TrustedIdentities)
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

	t.Run("raw_values_coerce_like_python", func(t *testing.T) {
		t.Parallel()
		// Python stores whatever TOML produced and converts at the use
		// sites: a bool gate takes the truthiness of any value, and an
		// int() at the use site of a string fails (0 in Go) except where
		// the caller catches the failure and falls back.
		updated := ApplyConfigData(base, map[string]any{
			"announce_on_start":  "yes",
			"max_nick_bytes":     "not-int",
			"max_msg_body_bytes": int64(400),
		})
		if !updated.AnnounceOnStart {
			t.Errorf("truthy string for a bool field must enable it")
		}
		if updated.MaxNickBytes != 0 {
			t.Errorf("unparseable string for an int field = %v, want 0 (Python int() fails)", updated.MaxNickBytes)
		}
		// The nick limit path catches the failure like normalize_nick and
		// falls back to its 32-byte default.
		if got := configIntOr(updated.rawConfigValue("max_nick_bytes"), defaultNickMaxBytes); got != 32 {
			t.Errorf("nick limit fallback = %v, want 32", got)
		}
		if updated.MaxMsgBodyBytes != 400 {
			t.Errorf("int64 int field not applied: %v", updated.MaxMsgBodyBytes)
		}
		if got := updated.rawConfigValue("max_nick_bytes"); got != "not-int" {
			t.Errorf("raw max_nick_bytes = %v, want the raw string", got)
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
		// G16.20 The truncation counts code points, not bytes: 81
		// two-byte runes (162 bytes) truncate at 77 runes.
		{strings.Repeat("é", 81), strings.Repeat("é", 77) + "..."},
		{strings.Repeat("é", 80), strings.Repeat("é", 80)},
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

// G16.1 TOML integer values pass through into the float config fields
// (Python's dataclasses.replace stores the raw value and the use sites
// float() it), and TOML floats truncate into the int fields the way
// Python's int() does. Golden: ConfigManager().apply_config_data(
// HubRuntimeConfig(), {"hub": {"ping_interval_s": 30, ...}}) keeps 30.
func TestApplyConfigDataTOMLIntPassthrough(t *testing.T) {
	t.Parallel()

	updated := ApplyConfigData(DefaultHubConfig(), map[string]any{"hub": map[string]any{
		"ping_interval_s":                int64(30),
		"ping_timeout_s":                 int64(15),
		"announce_period_s":              int64(300),
		"room_registry_prune_after_s":    int64(100),
		"room_registry_prune_interval_s": int64(50),
		"room_invite_timeout_s":          int64(60),
		"resource_expectation_ttl_s":     int64(45),
		"max_msg_body_bytes":             300.9,
		"max_nick_bytes":                 32.9,
		"max_rooms_per_session":          5.0,
		"rate_limit_msgs_per_minute":     240.5,
	}})

	intWants := []struct {
		name string
		got  float64
		want float64
	}{
		{"ping_interval_s", updated.PingIntervalS, 30},
		{"ping_timeout_s", updated.PingTimeoutS, 15},
		{"announce_period_s", updated.AnnouncePeriodS, 300},
		{"room_registry_prune_after_s", updated.RoomRegistryPruneAfterS, 100},
		{"room_registry_prune_interval_s", updated.RoomRegistryPruneIntervalS, 50},
		{"room_invite_timeout_s", updated.RoomInviteTimeoutS, 60},
		{"resource_expectation_ttl_s", updated.ResourceExpectationTTLs, 45},
	}
	for _, w := range intWants {
		if w.got != w.want {
			t.Errorf("%v = %v, want %v", w.name, w.got, w.want)
		}
	}
	floatWants := []struct {
		name string
		got  int
		want int
	}{
		{"max_msg_body_bytes", updated.MaxMsgBodyBytes, 300},
		{"max_nick_bytes", updated.MaxNickBytes, 32},
		{"max_rooms_per_session", updated.MaxRoomsPerSession, 5},
		{"rate_limit_msgs_per_minute", updated.RateLimitMsgsPerMinute, 240},
	}
	for _, w := range floatWants {
		if w.got != w.want {
			t.Errorf("%v = %v, want %v (Python int() truncates)", w.name, w.got, w.want)
		}
	}

	// The raw TOML values stay available for the raw-value use sites.
	if got := updated.rawConfigValue("ping_interval_s"); got != int64(30) {
		t.Errorf("raw ping_interval_s = %v (%T), want int64(30)", got, got)
	}
	if got := updated.rawConfigValue("max_msg_body_bytes"); got != 300.9 {
		t.Errorf("raw max_msg_body_bytes = %v, want 300.9", got)
	}
}

// G16.2 Boolean config fields follow Python truthiness: 1, "true", and
// even the string "false" are all truthy, while 0, "", and false are not.
func TestApplyConfigDataTruthinessBoolFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"int 1", int64(1), true},
		{"int 0", int64(0), false},
		{"string true", "true", true},
		{"string false is truthy", "false", true},
		{"empty string", "", false},
		{"real bool", true, true},
		{"real false bool", false, false},
		{"float 2.5", 2.5, true},
		{"float 0.0", 0.0, false},
		{"non-empty list", []any{"x"}, true},
		{"empty list", []any{}, false},
	}
	for _, field := range []string{"include_joined_member_list", "enable_resource_transfer"} {
		for _, tt := range tests {
			updated := ApplyConfigData(DefaultHubConfig(), map[string]any{
				"hub": map[string]any{field: tt.val},
			})
			var got bool
			switch field {
			case "include_joined_member_list":
				got = updated.IncludeJoinedMemberList
			case "enable_resource_transfer":
				got = updated.EnableResourceTransfer
			}
			if got != tt.want {
				t.Errorf("%v = %v: got %v, want %v", field, tt.name, got, tt.want)
			}
		}
	}
}

// G16.2 The legacy announce key coerces with bool() at apply time: the
// string "false" is a non-empty string and therefore TRUE, while 0 and ""
// are falsy.
func TestApplyConfigDataLegacyAnnounceTruthiness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"string false is truthy", "false", true},
		{"string yes", "yes", true},
		{"int 1", int64(1), true},
		{"int 0", int64(0), false},
		{"empty string", "", false},
		{"real false", false, false},
		{"real true", true, true},
	}
	for _, tt := range tests {
		updated := ApplyConfigData(DefaultHubConfig(), map[string]any{"announce": tt.val})
		if updated.AnnounceOnStart != tt.want {
			t.Errorf("announce = %v: got %v, want %v", tt.name, updated.AnnounceOnStart, tt.want)
		}
	}
}

// G16.2 A non-list trusted_identities value must not boot the hub with
// empty trust: Python's load_from_config iterates the string and every
// character fails identity-hash parsing, crashing the start. The Go port
// keeps the character split so the parse fails the same way.
func TestStartFailsOnNonListTrustedIdentities(t *testing.T) {
	t.Parallel()

	cfg := ApplyConfigData(DefaultHubConfig(), map[string]any{
		"hub": map[string]any{"trusted_identities": "abc"},
	})
	if !reflect.DeepEqual(cfg.TrustedIdentities, []string{"a", "b", "c"}) {
		t.Fatalf("TrustedIdentities = %v, want the character split", cfg.TrustedIdentities)
	}

	env := newHubTestEnv(t)
	env.setDestination(t)
	err := env.hub.TrustManager.LoadFromConfig(cfg.TrustedIdentities, nil)
	if err == nil {
		t.Fatal("a non-list trusted_identities must fail the trust load, not boot with empty trust")
	}

	// An empty string iterates to no entries and loads fine, like
	// Python's list("").
	cfg = ApplyConfigData(DefaultHubConfig(), map[string]any{
		"hub": map[string]any{"trusted_identities": ""},
	})
	env2 := newHubTestEnv(t)
	env2.setDestination(t)
	if err := env2.hub.TrustManager.LoadFromConfig(cfg.TrustedIdentities, nil); err != nil {
		t.Errorf("an empty trusted_identities string must load cleanly: %v", err)
	}
}
