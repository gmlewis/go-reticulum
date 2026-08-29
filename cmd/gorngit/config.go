// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// config.go implements the gorngit node configuration loader, mirroring
// the [rngit], [repositories], [aliases], and [access] sections of the
// default rngit config (RNS/Utilities/rngit/server.py
// __default_rngit_config__ + __apply_config). Stats and pages config is
// deferred to follow-up tasks.

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// nodeConfig is the parsed gorngit node configuration.
type nodeConfig struct {
	announceInterval time.Duration
	groups           map[string]string
	// access maps a group name to the comma-separated permission lines
	// from the [access] config section, applied on top of the group
	// .allowed file in updateGroupPermissions.
	access map[string][]string
	// aliases maps an alias name to a 32-char identity hash hex string,
	// from the [aliases] config section.
	aliases map[string]string
	// blockedIdentities is the list of identity hashes (hex or alias)
	// from the [rngit] blocked_identities setting, resolved via aliases
	// before use.
	blockedIdentities []string
	// nodeName is the human-readable node name shown on the page front
	// page, from [rngit] node_name (server.py:2203). Defaults to
	// "Anonymous Git Node".
	nodeName string
	// recordStats enables the stats subsystem, from [rngit] record_stats
	// (server.py:2206).
	recordStats bool
	// statsIgnored is the list of identity hashes (hex or alias) from
	// [rngit] stats_ignore_identities (server.py:2208), resolved via
	// aliases before use.
	statsIgnored []string
	// serveNomadnet enables the nomadnet-compatible page node, from
	// [pages] serve_nomadnet (server.py:2238). Defaults to false.
	serveNomadnet bool
	// unicodeIcons disables Nerd Font icons in favor of simpler unicode
	// glyphs, from [pages] unicode_icons (pages.py:165). Defaults to
	// false (Nerd Fonts on).
	unicodeIcons bool
}

// resolveNodeConfigDir resolves the effective node config directory. When
// configDir is empty it mirrors ReticulumGitNode.__init__ (server.py): /etc/rngit
// if it exists and holds a config file; otherwise ~/.config/rngit when that
// exists and holds a config file (in which case ~/.rngit/reticulum is used,
// as upstream); otherwise ~/.rngit.
func resolveNodeConfigDir(configDir string) (string, error) {
	if configDir != "" {
		return configDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home dir: %w", err)
	}
	if info, err := os.Stat("/etc/rngit"); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join("/etc/rngit", "config")); err == nil {
			return "/etc/rngit", nil
		}
	}
	configDirAlt := filepath.Join(home, ".config", "rngit")
	if info, err := os.Stat(configDirAlt); err == nil && info.IsDir() {
		if _, err := os.Stat(filepath.Join(configDirAlt, "config")); err == nil {
			return filepath.Join(home, ".rngit", "reticulum"), nil
		}
	}
	return filepath.Join(home, ".rngit"), nil
}

// defaultNodeConfig returns the baseline config used when no config file
// exists, mirroring __default_rngit_config__ (server.py). The announce
// interval defaults to 6 hours (360 minutes).
func defaultNodeConfig() *nodeConfig {
	return &nodeConfig{
		announceInterval: 360 * time.Minute,
		groups:           make(map[string]string),
		access:           make(map[string][]string),
		aliases:          make(map[string]string),
		nodeName:         "Anonymous Git Node",
	}
}

// loadNodeConfig loads the gorngit node config from configDir. If no config
// file exists, it writes the default config and returns it. Mirrors
// __create_default_config + __apply_config (server.py) for the sections needed
// by list/create.
func loadNodeConfig(configDir string) (*nodeConfig, error) {
	configPath := filepath.Join(configDir, "config")
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := defaultNodeConfig()
			if err := writeDefaultNodeConfig(configPath); err != nil {
				return nil, fmt.Errorf("could not write default config: %w", err)
			}
			return cfg, nil
		}
		return nil, fmt.Errorf("could not read config: %w", err)
	}
	return parseNodeConfig(string(data))
}

// parseNodeConfig parses the INI-style config text into a nodeConfig.
func parseNodeConfig(text string) (*nodeConfig, error) {
	cfg := defaultNodeConfig()
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}

		key, value, ok := splitConfigLine(line)
		if !ok {
			continue
		}

		switch section {
		case "rngit":
			if key == "announce_interval" {
				minutes, err := strconv.Atoi(strings.TrimSpace(value))
				if err == nil && minutes >= 0 {
					cfg.announceInterval = time.Duration(minutes) * time.Minute
				}
			}
			if key == "node_name" {
				cfg.nodeName = strings.TrimSpace(value)
			}
			if key == "record_stats" {
				if b, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
					cfg.recordStats = b
				}
			}
			if key == "stats_ignore_identities" {
				cfg.statsIgnored = append(cfg.statsIgnored, parseCommaList(value)...)
			}
			if key == "blocked_identities" {
				cfg.blockedIdentities = append(cfg.blockedIdentities, parseCommaList(value)...)
			}
		case "repositories":
			cfg.groups[key] = expandPath(value)
		case "aliases":
			cfg.aliases[key] = strings.TrimSpace(value)
		case "access":
			cfg.access[key] = parseCommaList(value)
		case "pages":
			if key == "serve_nomadnet" {
				if b, ok := parseConfigBool(value); ok {
					cfg.serveNomadnet = b
				}
			}
			if key == "unicode_icons" {
				if b, ok := parseConfigBool(value); ok {
					cfg.unicodeIcons = b
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("could not parse config: %w", err)
	}
	return cfg, nil
}

// parseCommaList splits a comma-separated config value into trimmed,
// non-empty entries, mirroring ConfigObj's as_list (server.py). Empty
// fields are dropped.
func parseCommaList(value string) []string {
	var out []string
	for field := range strings.SplitSeq(value, ",") {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

// parseConfigBool interprets a config boolean value the way ConfigObj's
// as_bool does (server.py): true/yes/on/1 -> true, false/no/off/0 -> false,
// case-insensitively. It returns ok=false for unrecognized values.
func parseConfigBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "on", "1":
		return true, true
	case "false", "no", "off", "0":
		return false, true
	}
	return false, false
}

// splitConfigLine splits a "key = value" config line.
func splitConfigLine(line string) (string, string, bool) {
	before, after, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(before), strings.TrimSpace(after), true
}

// expandPath expands a leading ~ in a path, mirroring os.path.expanduser.
func expandPath(path string) string {
	if path == "" {
		return path
	}
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			if path == "~" {
				return home
			}
			if strings.HasPrefix(path, "~/") {
				return filepath.Join(home, path[2:])
			}
		}
	}
	return path
}

// writeDefaultNodeConfig writes the default rngit config to configPath,
// mirroring __create_default_config (server.py).
func writeDefaultNodeConfig(configPath string) error {
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	const defaultConfig = `# This is the default gorngit config file.

[rngit]
# Automatic announce interval in minutes (6 hours by default).
announce_interval = 360

# You can block specific identities from any interaction with this node.
# blocked_identities = d31aeea49873006f13b3415520666a4e

[repositories]
# Define repository groups as group_name = /path/to/bare/repos
# internal = /path/to/directory/with/git/repositories
# public = /another/path/to/directory/with/git/repositories

[aliases]
# Define aliases for identity hashes as alias = IDENTITY_HASH (32 hex
# chars). These are used by the permissions system and identity
# resolution.
# alice = d09285e660cfe27cee6d9a0beb58b7e0

[access]
# Apply permissions for all repositories within a group, comma-separated
# permission lines applied on top of the group .allowed file.
# public = r:all, w:9710b86ba12c42d1d8f30f74fe509286
# internal = rw:9710b86ba12c42d1d8f30f74fe509286

[pages]
# You can run a nomadnet-compatible page node to serve repository
# information. Access permissions follow those configured per group
# and repository. The page server supports automatic markdown-to-micron
# conversion and optional syntax highlighting.
# serve_nomadnet = no

# Disable Nerd Font icons and use simpler (but more compatible) unicode
# icons instead.
# unicode_icons = yes

[logging]
# Valid log levels are 0 through 7 (4 is the default).
loglevel = 4
`
	return os.WriteFile(configPath, []byte(defaultConfig), 0o600)
}

// loadOrCreateIdentity loads an identity from identityPath or creates and
// persists a new one, mirroring __apply_config identity loading (server.py).
func loadOrCreateIdentity(identityPath string, logger *rns.Logger) (*rns.Identity, error) {
	if identity, err := rns.FromFile(identityPath, logger); err == nil && identity != nil {
		logger.Verbose("Repositories identity loaded from %v", identityPath)
		return identity, nil
	}
	identity, err := rns.NewIdentity(true, logger)
	if err != nil {
		return nil, fmt.Errorf("could not create identity: %w", err)
	}
	if err := identity.ToFile(identityPath); err != nil {
		return nil, fmt.Errorf("could not persist identity to %v: %w", identityPath, err)
	}
	logger.Verbose("Repositories identity generated and persisted to %v", identityPath)
	return identity, nil
}
