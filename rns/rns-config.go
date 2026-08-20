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

func applyBootstrapOnly(iface interfaces.Interface, bootstrapOnly bool) {
	setter, ok := iface.(interface {
		SetBootstrapOnly(bool)
	})
	if !ok {
		return
	}
	setter.SetBootstrapOnly(bootstrapOnly)
}

func applyInterfaceErrorPolicy(iface interfaces.Interface, enabled bool) {
	setter, ok := iface.(interface {
		SetPanicOnInterfaceErrorEnabled(bool)
	})
	if !ok {
		return
	}
	setter.SetPanicOnInterfaceErrorEnabled(enabled)
}

// interfaceContractConfig captures the per-interface routing-policy
// keys (RNS v1.3.7/1.4.1). gravity defaults to the instance-wide
// default_gravity; the remaining fields mirror Interface.__init__.
type interfaceContractConfig struct {
	gravity               int
	recursivePrs          bool
	announcesFromInternal bool
	announcesToInternal   *bool

	// announceRateTarget/grace/penalty mirror Python's announce_rate_*
	// (Reticulum.py:819-857,938-940). A nil pointer means "no rate limit"
	// (Python None); when transport is enabled the parser fills nil values
	// from the instance-wide default_ar_* (which themselves resolve to the
	// DEFAULT_AR_* class constants).
	announceRateTarget  *int
	announceRateGrace   *int
	announceRatePenalty *int
}

// contractConfigSetter is implemented by interfaces that accept the
// contract policy fields (BaseInterface and concrete types embedding it).
type contractConfigSetter interface {
	SetGravity(int)
	SetRecursivePrs(bool)
	SetAnnouncesFromInternal(bool)
	SetAnnouncesToInternal(*bool)
	SetAnnounceRateTarget(*int)
	SetAnnounceRateGrace(*int)
	SetAnnounceRatePenalty(*int)
}

// announceRateDefaults carries the instance-wide announce-rate defaults
// resolved by Reticulum._default_ar_target/penalty/grace (Reticulum.py:1145-
// 1152): each is the override-or-class-constant concrete value used to fill
// per-interface nil entries when transport is enabled.
type announceRateDefaults struct {
	target  int
	penalty int
	grace   int
}

// parseInterfaceContractConfig reads the per-interface gravity/recursive_prs/
// announces_from_internal/announces_to_internal keys, mirroring
// Reticulum.py:771-772,842-849. gravity inherits defaultGravity when unset;
// announces_from_internal defaults true; announces_to_internal defaults nil.
//
// It also parses the per-interface announce_rate_target/grace/penalty keys
// (Reticulum.py:819-857,938-940): target must be >0; grace/penalty >=0. When
// announce_rate_target is set, grace and penalty default to 0 if unset
// (Reticulum.py:831-832). When transport is enabled, any still-nil value is
// filled from the instance-wide announceRateDefaults (Reticulum.py:855-857);
// when transport is disabled, nil values remain nil (no rate limit).
func parseInterfaceContractConfig(sub *ConfigSection, defaultGravity int, arDefaults announceRateDefaults, transportEnabled bool) interfaceContractConfig {
	cfg := interfaceContractConfig{
		gravity:               defaultGravity,
		recursivePrs:          false,
		announcesFromInternal: true,
		announcesToInternal:   nil,
	}
	if sub != nil {
		if v, ok := sub.GetProperty("gravity"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				cfg.gravity = n
			}
		}
		if v, ok := sub.GetProperty("recursive_prs"); ok {
			cfg.recursivePrs = parseBoolLike(v)
		}
		if v, ok := sub.GetProperty("announces_from_internal"); ok {
			cfg.announcesFromInternal = parseBoolLike(v)
		}
		if v, ok := sub.GetProperty("announces_to_internal"); ok {
			b := parseBoolLike(v)
			cfg.announcesToInternal = &b
		}
		// Per-interface announce-rate keys (Reticulum.py:819-829).
		if v, ok := sub.GetProperty("announce_rate_target"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				nv := n
				cfg.announceRateTarget = &nv
			}
		}
		if v, ok := sub.GetProperty("announce_rate_grace"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
				nv := n
				cfg.announceRateGrace = &nv
			}
		}
		if v, ok := sub.GetProperty("announce_rate_penalty"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
				nv := n
				cfg.announceRatePenalty = &nv
			}
		}
	}

	// target set ⇒ grace/penalty default to 0 (Reticulum.py:831-832).
	if cfg.announceRateTarget != nil {
		if cfg.announceRateGrace == nil {
			zero := 0
			cfg.announceRateGrace = &zero
		}
		if cfg.announceRatePenalty == nil {
			zero := 0
			cfg.announceRatePenalty = &zero
		}
	}

	// When transport is enabled, fill any still-nil value from the
	// instance-wide defaults (Reticulum.py:855-857).
	if transportEnabled {
		if cfg.announceRateTarget == nil {
			nv := arDefaults.target
			cfg.announceRateTarget = &nv
		}
		if cfg.announceRatePenalty == nil {
			nv := arDefaults.penalty
			cfg.announceRatePenalty = &nv
		}
		if cfg.announceRateGrace == nil {
			nv := arDefaults.grace
			cfg.announceRateGrace = &nv
		}
	}

	return cfg
}

func applyInterfaceConfig(iface interfaces.Interface, mode int, ifac interfaces.IFACConfig, discovery interfaces.DiscoveryConfig, contract interfaceContractConfig, bootstrapOnly bool, panicOnInterfaceError bool) {
	applyInterfaceMode(iface, mode)
	applyIFACConfig(iface, ifac)
	applyDiscoveryConfig(iface, discovery)
	applyInterfaceContract(iface, contract)
	applyBootstrapOnly(iface, bootstrapOnly)
	applyInterfaceErrorPolicy(iface, panicOnInterfaceError)
}

// applyInterfaceContract applies the per-interface routing-policy
// fields when the interface accepts them. Spawned peers do not go through this
// path; they inherit the parent's policy at spawn time.
func applyInterfaceContract(iface interfaces.Interface, contract interfaceContractConfig) {
	setter, ok := iface.(contractConfigSetter)
	if !ok {
		return
	}
	setter.SetGravity(contract.gravity)
	setter.SetRecursivePrs(contract.recursivePrs)
	setter.SetAnnouncesFromInternal(contract.announcesFromInternal)
	setter.SetAnnouncesToInternal(contract.announcesToInternal)
	setter.SetAnnounceRateTarget(contract.announceRateTarget)
	setter.SetAnnounceRateGrace(contract.announceRateGrace)
	setter.SetAnnounceRatePenalty(contract.announceRatePenalty)
}

func applySpawnedInterfaceConfig(iface interfaces.Interface, mode int, ifac interfaces.IFACConfig, panicOnInterfaceError bool) {
	applyInterfaceMode(iface, mode)
	applyIFACConfig(iface, ifac)
	applyInterfaceErrorPolicy(iface, panicOnInterfaceError)
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

// parseBackboneFastFlapConfig reads the fast-flap blocking keys from a
// Backbone interface config section, applying Python's defaults when a key is
// absent (BackboneInterface.py:126-129, v1.3.9). fast_flapping_block_time is
// in minutes (Python multiplies by 60); the returned expiry is in seconds.
func parseBackboneFastFlapConfig(sub *ConfigSection) (block bool, threshold float64, grace int, expirySeconds float64) {
	block = interfaces.BackboneBlockFastFlapping
	threshold = interfaces.BackboneFastFlapThreshold
	grace = interfaces.BackboneFastFlapGrace
	expirySeconds = interfaces.BackboneFastFlapExpiry
	if sub == nil {
		return
	}
	if v, ok := sub.GetProperty("block_fast_flapping"); ok {
		block = parseBoolLike(v)
	}
	if v, ok := sub.GetProperty("fast_flapping_threshold"); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			threshold = f
		}
	}
	if v, ok := sub.GetProperty("fast_flapping_grace"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			grace = n
		}
	}
	if v, ok := sub.GetProperty("fast_flapping_block_time"); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			// Python: fast_flapping_block_time is in minutes -> seconds.
			expirySeconds = f * 60
		}
	}
	return
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
	case "internal":
		return interfaces.ModeInternal, true
	default:
		return 0, false
	}
}

func interfaceSupportsDiscovery(ifaceType string) bool {
	switch ifaceType {
	case "TCPInterface", "TCPClientInterface", "TCPServerInterface", "BackboneInterface", "I2PInterface", "KISSInterface", "RNodeInterface", "WeaveInterface":
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
			cfg.AnnounceInterval = max(time.Duration(minutes)*time.Minute, 5*time.Minute)
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
	if v, ok := sub.GetProperty("location_cmd"); ok {
		cfg.LocationCmd = strings.TrimSpace(v)
	}
	if v, ok := sub.GetProperty("discovery_frequency"); ok {
		cfg.Frequency = parseOptionalInt(v)
	}
	if v, ok := sub.GetProperty("discovery_bandwidth"); ok {
		cfg.Bandwidth = parseOptionalInt(v)
	}
	if v, ok := sub.GetProperty("discovery_channel"); ok {
		cfg.Channel = parseOptionalInt(v)
	}
	if v, ok := sub.GetProperty("discovery_modulation"); ok {
		cfg.Modulation = strings.TrimSpace(v)
	}

	// RNS/Reticulum.py (v1.3.9): when discovery is enabled, an interface
	// already in [MODE_GATEWAY, MODE_ACCESS_POINT, MODE_INTERNAL] keeps its
	// mode; any other mode is auto-reconfigured to access_point (RNode) or
	// gateway (everything else). MODE_INTERNAL joined the allowed set at
	// v1.3.9, so a discoverable internal-mode interface is preserved.
	if mode != interfaces.ModeGateway && mode != interfaces.ModeAccessPoint && mode != interfaces.ModeInternal {
		if ifaceType == "RNodeInterface" || ifaceType == "RNodeMultiInterface" {
			mode = interfaces.ModeAccessPoint
		} else {
			mode = interfaces.ModeGateway
		}
	}

	return cfg, mode
}
