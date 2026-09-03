// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"fmt"
	"strings"

	"github.com/gmlewis/go-reticulum/rrcd/toml"
)

// HubConfig mirrors Python's HubRuntimeConfig: every field keeps its
// declared Go type and the defaults match rrcd 0.3.2 exactly. Values from
// TOML flow in without type coercion; only values whose TOML type matches
// the declared Go type are applied.
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

// strPtr returns a pointer to a copy of s.
func strPtr(s string) *string { return &s }

// applyFlatKey applies one flattened config key to the config. It returns
// false for keys it does not handle (unknown keys are ignored by the
// caller, matching Python's allowed-field filtering).
func (c *HubConfig) applyFlatKey(key string, val any) {
	setBool := func(dst *bool) {
		if b, ok := val.(bool); ok {
			*dst = b
		}
	}
	setFloat := func(dst *float64) {
		if n, ok := val.(float64); ok {
			*dst = n
		}
	}
	setInt := func(dst *int) {
		if n, ok := val.(int64); ok {
			*dst = int(n)
		}
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
				*dst = strPtr(s)
			}
		}
	}
	setList := func(dst *[]string) {
		if items, ok := val.([]any); ok {
			out := make([]string, len(items))
			for i, item := range items {
				out[i] = fmt.Sprintf("%v", item)
			}
			*dst = out
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
			c.RoomRegistryPath = strPtr(s)
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
		setList(&c.TrustedIdentities)
	case "banned_identities":
		setList(&c.BannedIdentities)
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
	for k, v := range data {
		flat[k] = v
	}
	if hub, ok := data["hub"].(map[string]any); ok {
		for k, v := range hub {
			flat[k] = v
		}
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
			if b, ok := val.(bool); ok {
				cfg.AnnounceOnStart = b
			}
			continue
		}
		cfg.applyFlatKey(key, val)
	}
	return cfg
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
	if len(s) > 80 {
		s = s[:77] + "..."
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
	return map[string]any{
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
}

// configValuesEqual compares two reload-display values for equality,
// including nil-able strings and string lists.
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
	case nil:
		return b == nil
	}
	return a == b
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
