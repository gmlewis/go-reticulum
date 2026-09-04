// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"fmt"
	"maps"
	"strings"

	"github.com/gmlewis/go-reticulum/rrcd/toml"
)

// HubConfig mirrors Python's HubRuntimeConfig: every field keeps its
// declared Go type and the defaults match rrcd 0.3.2 exactly. Values from
// TOML flow in without type coercion (Python's dataclasses.replace stores
// the raw value), so numeric and boolean fields coerce leniently — ints
// become floats, floats truncate into ints, and booleans follow Python
// truthiness — while Raw preserves the untouched TOML values for the use
// sites that embed them verbatim.
type HubConfig struct {
	ConfigPath                     *string
	RoomRegistryPath               *string
	Configdir                      *string
	IdentityPath                   *string
	DestName                       string
	AnnounceOnStart                bool
	AnnouncePeriodS                float64
	HubName                        string
	Greeting                       *string
	TrustedIdentities              []string
	BannedIdentities               []string
	RoomRegistryPruneAfterS        float64
	RoomRegistryPruneIntervalS     float64
	RoomInviteTimeoutS             float64
	IncludeJoinedMemberList        bool
	MaxNickBytes                   int
	MaxRoomsPerSession             int
	MaxRoomNameBytes               int
	MaxMsgBodyBytes                int
	RateLimitMsgsPerMinute         int
	PingIntervalS                  float64
	PingTimeoutS                   float64
	MaxResourceBytes               int
	MaxPendingResourceExpectations int
	ResourceExpectationTTLs        float64
	EnableResourceTransfer         bool
	LogLevel                       string
	LogRNSLevel                    string
	LogConsole                     bool
	LogFile                        *string
	LogFormat                      string
	LogDatefmt                     *string

	// Raw holds the TOML values exactly as applied, keyed by the flat
	// config key. Python's use sites embed the raw field values (the
	// WELCOME limits map, reload summaries), so readers consult Raw and
	// fall back to the typed field when the key never came from TOML.
	Raw map[string]any
}

// rawConfigValue returns the raw TOML value recorded for a config key, or
// the typed field value when the key was never applied from TOML.
func (c *HubConfig) rawConfigValue(key string) any {
	if c.Raw != nil {
		if v, ok := c.Raw[key]; ok {
			return v
		}
	}
	return configToMap(*c)[key]
}

// setRawConfigValue records a programmatic (CLI) override in the raw map,
// initializing it when needed, so the raw-value readers stay in sync with
// the typed field.
func (c *HubConfig) setRawConfigValue(key string, v any) {
	if c.Raw == nil {
		c.Raw = map[string]any{}
	}
	c.Raw[key] = v
}

// OverrideRawConfigValue records a programmatic (CLI) override in the raw
// map, mirroring Python's dataclasses.replace of the field with the
// coerced CLI value.
func (c *HubConfig) OverrideRawConfigValue(key string, v any) {
	c.setRawConfigValue(key, v)
}

// DefaultHubConfig returns the exact default configuration of
// HubRuntimeConfig.
func DefaultHubConfig() HubConfig {
	return HubConfig{
		DestName:                       HubDestName,
		AnnounceOnStart:                true,
		AnnouncePeriodS:                0.0,
		HubName:                        "rrc",
		RoomRegistryPruneAfterS:        2592000.0,
		RoomRegistryPruneIntervalS:     3600.0,
		RoomInviteTimeoutS:             900.0,
		IncludeJoinedMemberList:        false,
		MaxNickBytes:                   32,
		MaxRoomsPerSession:             32,
		MaxRoomNameBytes:               64,
		MaxMsgBodyBytes:                350,
		RateLimitMsgsPerMinute:         240,
		PingIntervalS:                  0.0,
		PingTimeoutS:                   0.0,
		MaxResourceBytes:               262144,
		MaxPendingResourceExpectations: 8,
		ResourceExpectationTTLs:        30.0,
		EnableResourceTransfer:         true,
		LogLevel:                       "INFO",
		LogRNSLevel:                    "WARNING",
		LogConsole:                     true,
		LogFormat:                      "%(asctime)s %(levelname)s %(name)s[%(threadName)s]: %(message)s",
	}
}

// applyFlatKey applies one flattened config key to the config. It returns
// false for keys it does not handle (unknown keys are ignored by the
// caller, matching Python's allowed-field filtering).
func (c *HubConfig) applyFlatKey(key string, val any) {
	setBool := func(dst *bool) {
		// Python's bool fields hold the raw value and every use site is
		// a truthiness gate, so the truthy result is the faithful
		// coercion.
		*dst = configTruthy(val)
	}
	setFloat := func(dst *float64) {
		*dst = configFloat(val)
	}
	setInt := func(dst *int) {
		*dst = configInt(val)
	}
	setStr := func(dst *string) {
		if s, ok := val.(string); ok {
			*dst = s
		}
	}
	setOptStr := func(dst **string) {
		if s, ok := val.(string); ok {
			if s == "" {
				*dst = nil
			} else {
				*dst = new(s)
			}
		}
	}
	// setIdentityList mirrors Python's identity-list handling: lists are
	// converted with str() per element, strings are kept in the field and
	// character-iterated by both use sites (load_from_config's list() and
	// the reload loop), and any other value stays in the field where
	// list() raises. The Go list therefore carries the character split
	// for strings and the scalar's str() rendering for other values —
	// every entry then fails identity-hash parsing exactly like Python.
	setIdentityList := func(dst *[]string) {
		switch items := val.(type) {
		case []any:
			out := make([]string, len(items))
			for i, item := range items {
				out[i] = pythonScalarStr(item)
			}
			*dst = out
		case string:
			out := make([]string, 0, len(items))
			for _, r := range items {
				out = append(out, string(r))
			}
			*dst = out
		default:
			*dst = []string{pythonScalarStr(val)}
		}
	}

	switch key {
	case "config_path", "dest_name":
		// Never settable from configuration data.
	case "configdir":
		setOptStr(&c.Configdir)
	case "identity_path":
		setOptStr(&c.IdentityPath)
	case "room_registry_path":
		if s, ok := val.(string); ok {
			c.RoomRegistryPath = new(s)
		}
	case "announce_on_start":
		setBool(&c.AnnounceOnStart)
	case "announce_period_s":
		setFloat(&c.AnnouncePeriodS)
	case "hub_name":
		setStr(&c.HubName)
	case "greeting":
		setOptStr(&c.Greeting)
	case "trusted_identities":
		setIdentityList(&c.TrustedIdentities)
	case "banned_identities":
		setIdentityList(&c.BannedIdentities)
	case "room_registry_prune_after_s":
		setFloat(&c.RoomRegistryPruneAfterS)
	case "room_registry_prune_interval_s":
		setFloat(&c.RoomRegistryPruneIntervalS)
	case "room_invite_timeout_s":
		setFloat(&c.RoomInviteTimeoutS)
	case "include_joined_member_list":
		setBool(&c.IncludeJoinedMemberList)
	case "max_nick_bytes":
		setInt(&c.MaxNickBytes)
	case "max_rooms_per_session":
		setInt(&c.MaxRoomsPerSession)
	case "max_room_name_bytes":
		setInt(&c.MaxRoomNameBytes)
	case "max_msg_body_bytes":
		setInt(&c.MaxMsgBodyBytes)
	case "rate_limit_msgs_per_minute":
		setInt(&c.RateLimitMsgsPerMinute)
	case "ping_interval_s":
		setFloat(&c.PingIntervalS)
	case "ping_timeout_s":
		setFloat(&c.PingTimeoutS)
	case "max_resource_bytes":
		setInt(&c.MaxResourceBytes)
	case "max_pending_resource_expectations":
		setInt(&c.MaxPendingResourceExpectations)
	case "resource_expectation_ttl_s":
		setFloat(&c.ResourceExpectationTTLs)
	case "enable_resource_transfer":
		setBool(&c.EnableResourceTransfer)
	case "log_level":
		setStr(&c.LogLevel)
	case "log_rns_level":
		setStr(&c.LogRNSLevel)
	case "log_console":
		setBool(&c.LogConsole)
	case "log_file":
		setOptStr(&c.LogFile)
	case "log_format":
		setStr(&c.LogFormat)
	case "log_datefmt":
		setOptStr(&c.LogDatefmt)
	case "announce":
		// Legacy boolean; handled by the caller for precedence.
	}
}

// ApplyConfigData applies parsed TOML data to a base config the way
// ConfigManager.apply_config_data does: the [hub] table flattens over the
// top level, [logging] keys remap, config_path/dest_name can never be set,
// identity lists accept only lists, the legacy announce key applies only
// when announce_on_start was not set in the same data, and empty strings
// become nil for the optional path/text fields. Values flow through without
// type coercion.
func ApplyConfigData(base HubConfig, data map[string]any) HubConfig {
	cfg := base
	if data == nil {
		return cfg
	}
	flat := make(map[string]any, len(data))
	maps.Copy(flat, data)
	if hub, ok := data["hub"].(map[string]any); ok {
		maps.Copy(flat, hub)
	}
	if logTable, ok := data["logging"].(map[string]any); ok {
		for from, to := range map[string]string{
			"level":     "log_level",
			"rns_level": "log_rns_level",
			"console":   "log_console",
			"file":      "log_file",
			"format":    "log_format",
			"datefmt":   "log_datefmt",
		} {
			if v, ok := logTable[from]; ok {
				flat[to] = v
			}
		}
	}

	_, announceExplicit := flat["announce_on_start"]
	for key, val := range flat {
		if key == "announce" && !announceExplicit {
			// Python coerces the legacy key with bool() at apply time.
			coerced := configTruthy(val)
			cfg.AnnounceOnStart = coerced
			cfg.setRawConfigValue("announce_on_start", coerced)
			continue
		}
		if !isAllowedConfigKey(key) {
			continue
		}
		cfg.applyFlatKey(key, val)
		cfg.setRawConfigValue(key, val)
	}
	return cfg
}

// isAllowedConfigKey reports whether a flattened key is a HubRuntimeConfig
// field (config_path and dest_name can never be set from config data).
func isAllowedConfigKey(key string) bool {
	switch key {
	case "configdir", "identity_path", "room_registry_path", "announce_on_start",
		"announce_period_s", "hub_name", "greeting", "trusted_identities",
		"banned_identities", "room_registry_prune_after_s",
		"room_registry_prune_interval_s", "room_invite_timeout_s",
		"include_joined_member_list", "max_nick_bytes", "max_rooms_per_session",
		"max_room_name_bytes", "max_msg_body_bytes", "rate_limit_msgs_per_minute",
		"ping_interval_s", "ping_timeout_s", "max_resource_bytes",
		"max_pending_resource_expectations", "resource_expectation_ttl_s",
		"enable_resource_transfer", "log_level", "log_rns_level", "log_console",
		"log_file", "log_format", "log_datefmt":
		return true
	}
	return false
}

// FormatReloadValue formats a config value for display in reload summaries:
// nil → "(none)", bool/int/float → Python str(), lists → "len=N", strings
// whitespace-collapsed and truncated to 77 characters plus "..." when over
// 80.
func FormatReloadValue(v any) string {
	if v == nil {
		return "(none)"
	}
	if p, ok := v.(*string); ok {
		if p == nil {
			return "(none)"
		}
		v = *p
	}
	switch n := v.(type) {
	case bool:
		if n {
			return "True"
		}
		return "False"
	case int:
		return fmt.Sprintf("%v", n)
	case int64:
		return fmt.Sprintf("%v", n)
	case uint64:
		return fmt.Sprintf("%v", n)
	case float64:
		// Python str() for floats matches repr in Python 3.
		return toml.FormatFloat(n)
	case []string:
		return fmt.Sprintf("len=%v", len(n))
	case []any:
		return fmt.Sprintf("len=%v", len(n))
	}
	s := fmt.Sprintf("%v", v)
	fields := strings.Fields(s)
	s = strings.Join(fields, " ")
	// Python truncates characters, so the slice must count code points
	// and never split a UTF-8 sequence.
	if runes := []rune(s); len(runes) > 80 {
		s = string(runes[:77]) + "..."
	}
	return s
}

// DiffConfigSummary lists the differences between two configs as
// "key: old -> new" lines sorted by key, with config_path excluded (the
// dest_name field never changes so it never appears).
func DiffConfigSummary(oldCfg, newCfg HubConfig) []string {
	oldMap := configToMap(oldCfg)
	newMap := configToMap(newCfg)
	delete(oldMap, "config_path")
	delete(newMap, "config_path")

	keys := make([]string, 0, len(newMap))
	for k := range newMap {
		keys = append(keys, k)
	}
	sortStrings(keys)

	changed := make([]string, 0)
	for _, k := range keys {
		if configValuesEqual(oldMap[k], newMap[k]) {
			continue
		}
		changed = append(changed, fmt.Sprintf("%v: %v -> %v",
			k, FormatReloadValue(oldMap[k]), FormatReloadValue(newMap[k])))
	}
	return changed
}

// configToMap flattens a config into a comparable map of reload-display
// values.
func configToMap(c HubConfig) map[string]any {
	out := map[string]any{
		"config_path":                       c.ConfigPath,
		"room_registry_path":                c.RoomRegistryPath,
		"configdir":                         c.Configdir,
		"identity_path":                     c.IdentityPath,
		"dest_name":                         c.DestName,
		"announce_on_start":                 c.AnnounceOnStart,
		"announce_period_s":                 c.AnnouncePeriodS,
		"hub_name":                          c.HubName,
		"greeting":                          c.Greeting,
		"trusted_identities":                c.TrustedIdentities,
		"banned_identities":                 c.BannedIdentities,
		"room_registry_prune_after_s":       c.RoomRegistryPruneAfterS,
		"room_registry_prune_interval_s":    c.RoomRegistryPruneIntervalS,
		"room_invite_timeout_s":             c.RoomInviteTimeoutS,
		"include_joined_member_list":        c.IncludeJoinedMemberList,
		"max_nick_bytes":                    c.MaxNickBytes,
		"max_rooms_per_session":             c.MaxRoomsPerSession,
		"max_room_name_bytes":               c.MaxRoomNameBytes,
		"max_msg_body_bytes":                c.MaxMsgBodyBytes,
		"rate_limit_msgs_per_minute":        c.RateLimitMsgsPerMinute,
		"ping_interval_s":                   c.PingIntervalS,
		"ping_timeout_s":                    c.PingTimeoutS,
		"max_resource_bytes":                c.MaxResourceBytes,
		"max_pending_resource_expectations": c.MaxPendingResourceExpectations,
		"resource_expectation_ttl_s":        c.ResourceExpectationTTLs,
		"enable_resource_transfer":          c.EnableResourceTransfer,
		"log_level":                         c.LogLevel,
		"log_rns_level":                     c.LogRNSLevel,
		"log_console":                       c.LogConsole,
		"log_file":                          c.LogFile,
		"log_format":                        c.LogFormat,
		"log_datefmt":                       c.LogDatefmt,
	}
	// Keys applied from TOML carry their raw values: Python's asdict
	// shows the raw field values, so the diff compares and renders those.
	if c.Raw != nil {
		for key, val := range c.Raw {
			if _, allowed := out[key]; allowed {
				out[key] = val
			}
		}
	}
	return out
}

// configValuesEqual compares two reload-display values for equality,
// including nil-able strings and string lists; numerics compare across
// the int/float split the way Python's == does (30 == 30.0).
func configValuesEqual(a, b any) bool {
	as, aok := a.(*string)
	bs, bok := b.(*string)
	if aok || bok {
		if !aok || !bok {
			return false
		}
		if as == nil || bs == nil {
			return as == bs
		}
		return *as == *bs
	}
	switch av := a.(type) {
	case []string:
		bv, ok := b.([]string)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if av[i] != bv[i] {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !configValuesEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	case nil:
		return b == nil
	}
	if an, aok := configNumber(a); aok {
		if bn, bok := configNumber(b); bok {
			return an == bn
		}
		return false
	}
	return a == b
}

// configNumber converts a numeric scalar to float64 for Python-style
// cross-type equality (bools count as 1/0).
func configNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
