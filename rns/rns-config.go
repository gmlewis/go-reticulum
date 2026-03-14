// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

func hasConfigFile(dir string) bool {
	dirInfo, err := os.Stat(dir)
	if err != nil || !dirInfo.IsDir() {
		return false
	}

	configInfo, err := os.Stat(filepath.Join(dir, "config"))
	if err != nil || configInfo.IsDir() {
		return false
	}

	return true
}

func chooseConfigDir(explicit, home string, hasConfig func(string) bool) string {
	if explicit != "" {
		return explicit
	}

	if hasConfig(systemConfigDir) {
		return systemConfigDir
	}

	userConfigDir := filepath.Join(home, ".config", "reticulum")
	if hasConfig(userConfigDir) {
		return userConfigDir
	}

	return filepath.Join(home, ".reticulum")
}

func resolveConfigDir(configDir string) (string, error) {
	if configDir != "" {
		return configDir, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return chooseConfigDir("", home, hasConfigFile), nil
}

func ensureStartupLayout(configDir string) error {
	requiredDirs := []string{
		filepath.Join(configDir, "storage"),
		filepath.Join(configDir, "storage", "cache"),
		filepath.Join(configDir, "storage", "cache", "announces"),
		filepath.Join(configDir, "storage", "resources"),
		filepath.Join(configDir, "storage", "identities"),
		filepath.Join(configDir, "storage", "blackhole"),
		filepath.Join(configDir, "interfaces"),
	}

	for _, dir := range requiredDirs {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}

	return nil
}

func parseIFACConfig(sub *ConfigSection) interfaces.IFACConfig {
	cfg := interfaces.IFACConfig{}
	if sub == nil {
		return cfg
	}

	if v, ok := sub.GetProperty("ifac_netname"); ok {
		cfg.NetName = v
	}
	if v, ok := sub.GetProperty("network_name"); ok && cfg.NetName == "" {
		cfg.NetName = v
	}
	if v, ok := sub.GetProperty("networkname"); ok && cfg.NetName == "" {
		cfg.NetName = v
	}
	if v, ok := sub.GetProperty("ifac_netkey"); ok {
		cfg.NetKey = v
	}
	if v, ok := sub.GetProperty("pass_phrase"); ok && cfg.NetKey == "" {
		cfg.NetKey = v
	}
	if v, ok := sub.GetProperty("passphrase"); ok && cfg.NetKey == "" {
		cfg.NetKey = v
	}
	if v, ok := sub.GetProperty("ifac_size"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			if n >= IFACMinSize*8 {
				cfg.Size = n / 8
			} else if n > 0 {
				cfg.Size = n
			}
		}
	}

	cfg.Enabled = cfg.NetName != "" || cfg.NetKey != ""
	return cfg
}

func applyIFACConfig(iface interfaces.Interface, cfg interfaces.IFACConfig) {
	setter, ok := iface.(interface {
		SetIFACConfig(interfaces.IFACConfig)
	})
	if !ok {
		return
	}
	setter.SetIFACConfig(cfg)
}

func parseListProperty(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}

	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")

	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		out = append(out, item)
	}

	if len(out) == 0 && v != "" {
		return []string{v}
	}

	return out
}

func parseBoolLike(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return false
	}
}
