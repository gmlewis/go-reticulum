// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the byte-exact first-run templates the daemon writes when
// its state files are missing. The template text is copied verbatim from the
// Python rrcd CLI bootstrap; the two path lines are emitted by interpolating
// Python-repr-quoted paths.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd"
	"github.com/gmlewis/go-reticulum/rrcd/toml"
)

// defaultConfigTemplate is the first-run rrcd.toml template. The markers
// "{{identity_path}}" and "{{room_registry_path}}" are replaced with the
// Python-repr-quoted paths at render time; every other byte is fixed.
const defaultConfigTemplate = `# rrcd configuration (TOML)
#
# This file was created on first run.
# Edit it, then start rrcd again.

[hub]

# Optional: Reticulum configuration directory.
# If left unset, Reticulum will choose its default (usually ~/.reticulum).
configdir = ""

# Where rrcd stores its persistent identity (Reticulum Identity file).
identity_path = {{identity_path}}

# Separate room registry file (registered rooms, topics, modes, bans, etc).
# This file is maintained by rrcd. You can edit it manually, but keep it valid TOML.
# A running hub can reload both rrcd.toml and rooms.toml with the /reload command.
room_registry_path = {{room_registry_path}}

# The hub destination namespace is fixed for client discovery.
# Hubs always announce on 'rrc.hub'.

# Announcing (Reticulum destination announces)
#
# announce_on_start: send a single announce right after startup.
# announce_period_s: if >0, periodically re-announce.
# To disable announcing entirely, set:
#   announce_on_start = false
#   announce_period_s = 0.0
announce_on_start = true
announce_period_s = 0.0

# Hub identity fields.
hub_name = "rrc"
greeting = ""

# Note: The hub 'greeting' is the MOTD (message of the day) delivered after WELCOME.
# If it exceeds the link MTU, it will be sent via RNS.Resource for reliable transfer.

# Operator / moderation
#
# trusted_identities: list of Reticulum Identity hashes (hex) allowed to run
# operator commands.
# banned_identities: list of Identity hashes (hex) that will be disconnected.
trusted_identities = []
banned_identities = []

# Registered-room pruning.
# Only applies to registered rooms with no connected members.
room_registry_prune_after_s = 2592000
room_registry_prune_interval_s = 3600.0

# Keyed-room invites.
# Room operators can use /invite to let a user join a +k room without the key.
# Invites are removed on join or after this timeout.
room_invite_timeout_s = 900.0

# Optional behaviors.
include_joined_member_list = false

# Limits.
# These limits help mitigate abuse and resource exhaustion, but can be adjusted
# based on your use case.
#
# N.B. max_msg_body_bytes should not allow messages so large that they cannot
# fit within the link MTU after UTF-8 encoding and envelope overhead. The
# default of 350 bytes is a safe choice for the default Reticulum MTU of 500.
max_nick_bytes = 32
max_room_name_bytes = 64
max_msg_body_bytes = 350
max_rooms_per_session = 32
rate_limit_msgs_per_minute = 240

# Hub-initiated liveness checks (0 disables).
ping_interval_s = 0.0
ping_timeout_s = 0.0

# Large payload transfer via RNS.Resource
#
# When a message exceeds the link MTU, rrcd can use RNS.Resource for reliable
# transfer instead of manual chunking. A small RESOURCE_ENVELOPE is sent first,
# followed by the payload as an RNS.Resource.
#
# enable_resource_transfer: enable/disable feature (default: true)
# max_resource_bytes: maximum size for a single resource (default: 256 KiB)
# max_pending_resource_expectations: max pending expectations per link (default: 8)
# resource_expectation_ttl_s: how long to wait for announced resource (default: 30s)
enable_resource_transfer = true
max_resource_bytes = 262144
max_pending_resource_expectations = 8
resource_expectation_ttl_s = 30.0

[logging]

# Log level for rrcd itself.
level = "INFO"

# Log level for Reticulum/RNS Python logging (if used by your install).
rns_level = "WARNING"

# Log to stderr (systemd/journald friendly).
console = true

# Optional file path for logs (leave empty to disable).
file = ""

# Log format and optional date format.
format = "%(asctime)s %(levelname)s %(name)s[%(threadName)s]: %(message)s"
datefmt = ""
`

// defaultRoomsTemplate is the first-run rooms.toml template, byte-identical to the Python original (sha256 79ea3400117e265b0513c2fc0f86059cf20a8a6d207f8325e5eed2a6023b633b, 1081 bytes).
const defaultRoomsTemplate = `# rrcd room registry (TOML)
#
# This file stores registered rooms and their moderation state.
# It is maintained by rrcd and may be updated while rrcd is running.
#
# Schema
# ------
#
# Each registered room is a table under [rooms]. Room names are TOML keys.
# If your room name contains spaces or punctuation, quote it:
#
#   [rooms."my room"]
#
# Supported keys per room:
#
# - founder:      string, hex Reticulum Identity hash
# - topic:        string (optional)
# - moderated:    bool (defaults false)
# - operators:    list of string identity hashes (hex)
# - voiced:       list of string identity hashes (hex)
# - bans:         list of string identity hashes (hex)
# - invited:      table mapping identity hash (hex) -> expiry unix timestamp seconds
# - last_used_ts: float unix timestamp seconds (used for pruning; optional)
#
# Example
# -------
#
# [rooms."lobby"]
# founder = "0123abcd..."
# topic = "Welcome"
# moderated = false
# operators = ["0123abcd..."]
# voiced = []
# bans = []
# invited = { "89abcdef..." = 1730003600.0 }
# last_used_ts = 1730000000.0

[rooms]
`

// defaultRoomsContent renders the first-run rooms.toml template bytes.
func defaultRoomsContent() string { return defaultRoomsTemplate }

// defaultConfigContent renders the first-run rrcd.toml bytes for the given
// identity and room registry paths.
func defaultConfigContent(identityPath, roomRegistryPath string) string {
	return strings.NewReplacer(
		"{{identity_path}}", pythonReprString(identityPath),
		"{{room_registry_path}}", pythonReprString(roomRegistryPath),
	).Replace(defaultConfigTemplate)
}

// pythonReprString renders a path the way Python's repr would quote it inside
// the template (single quotes preferred, double quotes when the value itself
// contains a single quote, backslash escapes for control characters).
func pythonReprString(s string) string {
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = "\""
	}
	var sb strings.Builder
	sb.WriteString(quote)
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			if r < 0x20 {
				sb.WriteString(fmt.Sprintf("\\x%02x", r))
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteString(quote)
	return sb.String()
}

// ensurePrivateDir creates the directory (with parents) and tightens its
// mode to 0o700 best-effort, mirroring ensure_private_dir.
func ensurePrivateDir(path string) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return
	}
	_ = os.Chmod(path, 0o700)
}

// writeDefaultConfig writes the first-run rrcd.toml with the interpolated
// identity path, mirroring _write_default_config.
func writeDefaultConfig(configPath, identityPath string) {
	if dir := filepath.Dir(configPath); dir != "" {
		ensurePrivateDir(dir)
	}
	if dir := filepath.Dir(identityPath); dir != "" {
		ensurePrivateDir(dir)
	}
	roomRegistryPath := rrcd.DefaultRoomRegistryPath()
	content := defaultConfigContent(identityPath, roomRegistryPath)
	// The write never creates the parent quietly; os.WriteFile fails on
	// a missing parent exactly like Python's open() would.
	_ = os.WriteFile(configPath, []byte(content), 0o644)
}

// ensureFirstRunFiles creates any missing state files and reports whether
// anything was created, mirroring _ensure_first_run_files.
func ensureFirstRunFiles(configPath, identityPath, roomRegistryPath string, newIdentity func(string) error) bool {
	createdAny := false

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		writeDefaultConfig(configPath, identityPath)
		createdAny = true
	}

	if _, err := os.Stat(identityPath); os.IsNotExist(err) {
		if dir := filepath.Dir(identityPath); dir != "" {
			ensurePrivateDir(dir)
		}
		if newIdentity == nil {
			newIdentity = writeNewIdentity
		}
		if err := newIdentity(identityPath); err == nil {
			createdAny = true
		}
	}

	if roomRegistryPath != "" {
		if _, err := os.Stat(roomRegistryPath); os.IsNotExist(err) {
			if dir := filepath.Dir(roomRegistryPath); dir != "" {
				ensurePrivateDir(dir)
			}
			if err := os.WriteFile(roomRegistryPath, []byte(defaultRoomsContent()), 0o600); err == nil {
				_ = os.Chmod(roomRegistryPath, 0o600)
				createdAny = true
			}
		}
	}

	return createdAny
}

// writeNewIdentity creates a fresh RNS identity and stores its private key
// at path with mode 0o600.
func writeNewIdentity(path string) error {
	identity, err := rns.NewIdentity(true, rns.NewLogger())
	if err != nil {
		return err
	}
	if err := identity.ToFile(path); err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

// firstRunMessage renders the stderr first-run notice with the go-prefix
// self-reference.
func firstRunMessage(configPath, identityPath, roomRegistryPath string) string {
	return "Created default gorrcd files. Edit the configuration before starting:\n" +
		"- Config:   " + configPath + "\n" +
		"- Identity: " + identityPath + "\n" +
		"- Rooms:    " + roomRegistryPath + "\n" +
		"\nThen re-run gorrcd.\n"
}

// loadConfigTOMLFile reads and parses an rrcd.toml file into the nested
// map ApplyConfigData consumes.
func loadConfigTOMLFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := toml.Parse(string(data))
	if err != nil {
		return nil, err
	}
	return rrcd.ConfigDataFromDoc(doc), nil
}
