// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// config.go implements a minimal gorngit node configuration loader,
// mirroring the [rngit] and [repositories] sections of the default rngit
// config (RNS/Utilities/rngit/server.py __default_rngit_config__). Full
// permissions/aliases/stats/pages config is deferred to follow-up tasks.

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
}

// defaultNodeConfig returns the baseline config used when no config file
// exists, mirroring __default_rngit_config__ (server.py). The announce
// interval defaults to 6 hours (360 minutes).
func defaultNodeConfig() *nodeConfig {
	return &nodeConfig{
		announceInterval: 360 * time.Minute,
		groups:           make(map[string]string),
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
		case "repositories":
			cfg.groups[key] = expandPath(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("could not parse config: %w", err)
	}
	return cfg, nil
}

// splitConfigLine splits a "key = value" config line.
func splitConfigLine(line string) (string, string, bool) {
	idx := strings.Index(line, "=")
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
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

[repositories]
# Define repository groups as group_name = /path/to/bare/repos
# internal = /path/to/directory/with/git/repositories
# public = /another/path/to/directory/with/git/repositories

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
