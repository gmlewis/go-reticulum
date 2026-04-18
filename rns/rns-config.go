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
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

const systemConfigDir = "/etc/reticulum"

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

func applyInterfaceMode(iface interfaces.Interface, mode int) {
	setter, ok := iface.(interface {
		SetMode(int)
	})
	if !ok {
		return
	}
	setter.SetMode(mode)
}

func applyDiscoveryConfig(iface interfaces.Interface, cfg interfaces.DiscoveryConfig) {
	setter, ok := iface.(interface {
		SetDiscoveryConfig(interfaces.DiscoveryConfig)
	})
	if !ok {
		return
	}
	setter.SetDiscoveryConfig(cfg)
}

func applyInterfaceConfig(iface interfaces.Interface, mode int, ifac interfaces.IFACConfig, discovery interfaces.DiscoveryConfig) {
	applyInterfaceMode(iface, mode)
	applyIFACConfig(iface, ifac)
	applyDiscoveryConfig(iface, discovery)
}

func applySpawnedInterfaceConfig(iface interfaces.Interface, mode int, ifac interfaces.IFACConfig) {
	applyInterfaceMode(iface, mode)
	applyIFACConfig(iface, ifac)
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

func parseOptionalFloat64(v string) *float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return nil
	}
	return &n
}

func parseOptionalInt(v string) *int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return nil
	}
	return &n
}

func parseInterfaceMode(sub *ConfigSection, ifaceType string) int {
	if sub == nil {
		return interfaces.ModeFull
	}
	if v, ok := sub.GetProperty("interface_mode"); ok {
		if mode, ok := parseInterfaceModeValue(v); ok {
			return mode
		}
	}
	if v, ok := sub.GetProperty("mode"); ok {
		trimmed := strings.TrimSpace(strings.ToLower(v))
		if ifaceType == "TCPInterface" && (trimmed == "client" || trimmed == "listen" || trimmed == "server") {
			return interfaces.ModeFull
		}
		if mode, ok := parseInterfaceModeValue(trimmed); ok {
			return mode
		}
	}
	return interfaces.ModeFull
}

func parseInterfaceModeValue(v string) (int, bool) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "full":
		return interfaces.ModeFull, true
	case "access_point", "accesspoint", "ap":
		return interfaces.ModeAccessPoint, true
	case "pointtopoint", "ptp":
		return interfaces.ModePointToPoint, true
	case "roaming":
		return interfaces.ModeRoaming, true
	case "boundary":
		return interfaces.ModeBoundary, true
	case "gateway", "gw":
		return interfaces.ModeGateway, true
	default:
		return 0, false
	}
}

func interfaceSupportsDiscovery(ifaceType string) bool {
	switch ifaceType {
	case "TCPInterface", "TCPClientInterface", "TCPServerInterface", "BackboneInterface", "I2PInterface", "RNodeInterface", "WeaveInterface":
		return true
	default:
		return false
	}
}

func parseDiscoveryConfig(sub *ConfigSection, ifaceType string, mode int) (interfaces.DiscoveryConfig, int) {
	cfg := interfaces.DiscoveryConfig{
		SupportsDiscovery: interfaceSupportsDiscovery(ifaceType),
	}
	if sub == nil {
		return cfg, mode
	}

	if v, ok := sub.GetProperty("discoverable"); ok {
		cfg.Discoverable = parseBoolLike(v)
	}
	if !cfg.Discoverable {
		return cfg, mode
	}

	if v, ok := sub.GetProperty("announce_interval"); ok {
		if minutes, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && minutes > 0 {
			cfg.AnnounceInterval = time.Duration(minutes) * time.Minute
			if cfg.AnnounceInterval < 5*time.Minute {
				cfg.AnnounceInterval = 5 * time.Minute
			}
		}
	}
	if cfg.AnnounceInterval == 0 {
		cfg.AnnounceInterval = 6 * time.Hour
	}

	if v, ok := sub.GetProperty("discovery_stamp_value"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			cfg.StampValue = n
		}
	}
	if v, ok := sub.GetProperty("discovery_name"); ok {
		cfg.Name = strings.TrimSpace(v)
	}
	if v, ok := sub.GetProperty("discovery_encrypt"); ok {
		cfg.Encrypt = parseBoolLike(v)
	}
	if v, ok := sub.GetProperty("reachable_on"); ok {
		cfg.ReachableOn = strings.TrimSpace(v)
	}
	if v, ok := sub.GetProperty("publish_ifac"); ok {
		cfg.PublishIFAC = parseBoolLike(v)
	}
	if v, ok := sub.GetProperty("latitude"); ok {
		cfg.Latitude = parseOptionalFloat64(v)
	}
	if v, ok := sub.GetProperty("longitude"); ok {
		cfg.Longitude = parseOptionalFloat64(v)
	}
	if v, ok := sub.GetProperty("height"); ok {
		cfg.Height = parseOptionalFloat64(v)
	}
	if v, ok := sub.GetProperty("discovery_frequency"); ok {
		cfg.Frequency = parseOptionalInt(v)
	}
	if v, ok := sub.GetProperty("discovery_bandwidth"); ok {
		cfg.Bandwidth = parseOptionalInt(v)
	}
	if v, ok := sub.GetProperty("discovery_modulation"); ok {
		cfg.Modulation = strings.TrimSpace(v)
	}

	if mode != interfaces.ModeGateway && mode != interfaces.ModeAccessPoint {
		if ifaceType == "RNodeInterface" || ifaceType == "RNodeMultiInterface" {
			mode = interfaces.ModeAccessPoint
		} else {
			mode = interfaces.ModeGateway
		}
	}

	return cfg, mode
}
