// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func resolveLogLevel(configLogLevel int, verbosity int, quietness int) int {
	res := configLogLevel
	if res == -1 {
		res = 3
	}
	if verbosity != 0 || quietness != 0 {
		res = res + verbosity - quietness
	}
	return res
}

func ensureConfig(configDir string) error {
	configPath := filepath.Join(configDir, "config")
	if isFile(configPath) {
		return nil
	}

	log.Printf("Could not load config file, creating default configuration file...")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create config directory %q: %w", configDir, err)
	}

	if err := os.WriteFile(configPath, []byte(defaultLXMDaemonConfig), 0644); err != nil {
		return fmt.Errorf("write default config to %q: %w", configPath, err)
	}

	log.Printf("Default config file created. Make any necessary changes in %v and restart golxmd if needed.", configPath)
	time.Sleep(1500 * time.Millisecond)
	return nil
}

func parseList(s string) []string {
	parts := strings.Split(s, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

type activeConfig struct {
	DisplayName                        string
	PeerAnnounceAtStart                bool
	PeerAnnounceInterval               *int
	DeliveryTransferMaxAcceptedSize    float64
	OnInbound                          string
	EnablePropagationNode              bool
	NodeName                           string
	AuthRequired                       bool
	NodeAnnounceAtStart                bool
	Autopeer                           bool
	AutopeerMaxdepth                   *int
	NodeAnnounceInterval               *int
	MessageStorageLimit                float64
	PropagationTransferMaxAcceptedSize float64
	PropagationSyncMaxAcceptedSize     float64
	PropagationStampCostTarget         int
	PropagationStampCostFlexibility    int
	PeeringCost                        int
	RemotePeeringCostMax               int
	PrioritisedLXMFDestinations        []string
	ControlAllowedIdentities           []string
	StaticPeers                        [][]byte
	MaxPeers                           *int
	FromStaticOnly                     bool
	LogLevel                           int
	IgnoredLXMFDestinations            [][]byte
	AllowedIdentities                  [][]byte
}

func applyConfig(cfg map[string]map[string]string) (*activeConfig, error) {
	ac := &activeConfig{
		DisplayName:                        "Anonymous Peer",
		PeerAnnounceAtStart:                false,
		PeerAnnounceInterval:               nil,
		DeliveryTransferMaxAcceptedSize:    1000,
		OnInbound:                          "",
		EnablePropagationNode:              false,
		NodeName:                           "",
		AuthRequired:                       false,
		NodeAnnounceAtStart:                false,
		Autopeer:                           true,
		AutopeerMaxdepth:                   nil,
		NodeAnnounceInterval:               nil,
		MessageStorageLimit:                500,
		PropagationTransferMaxAcceptedSize: 256,
		PropagationSyncMaxAcceptedSize:     256 * 40,
		PropagationStampCostTarget:         16,
		PropagationStampCostFlexibility:    3,  // LXMF.LXMRouter.PROPAGATION_COST_FLEX
		PeeringCost:                        18, // LXMF.LXMRouter.PEERING_COST
		RemotePeeringCostMax:               26, // LXMF.LXMRouter.MAX_PEERING_COST
		PrioritisedLXMFDestinations:        []string{},
		ControlAllowedIdentities:           []string{},
		StaticPeers:                        [][]byte{},
		MaxPeers:                           nil,
		FromStaticOnly:                     false,
		LogLevel:                           4,
		IgnoredLXMFDestinations:            [][]byte{},
		AllowedIdentities:                  [][]byte{},
	}

	// [lxmf]
	if section, ok := cfg["lxmf"]; ok {
		if val, ok := section["display_name"]; ok {
			ac.DisplayName = val
		}
		if val, ok := section["announce_at_start"]; ok {
			ac.PeerAnnounceAtStart = parseBool(val)
		}
		if val, ok := section["announce_interval"]; ok {
			i := parseInt(val) * 60
			ac.PeerAnnounceInterval = &i
		}
		if val, ok := section["delivery_transfer_max_accepted_size"]; ok {
			ac.DeliveryTransferMaxAcceptedSize = parseFloat(val)
			if ac.DeliveryTransferMaxAcceptedSize < 0.38 {
				ac.DeliveryTransferMaxAcceptedSize = 0.38
			}
		}
		if val, ok := section["on_inbound"]; ok {
			ac.OnInbound = val
		}
	}

	// [propagation]
	if section, ok := cfg["propagation"]; ok {
		if val, ok := section["enable_node"]; ok {
			ac.EnablePropagationNode = parseBool(val)
		}
		if val, ok := section["node_name"]; ok {
			ac.NodeName = val
		}
		if val, ok := section["auth_required"]; ok {
			ac.AuthRequired = parseBool(val)
		}
		if val, ok := section["announce_at_start"]; ok {
			ac.NodeAnnounceAtStart = parseBool(val)
		}
		if val, ok := section["autopeer"]; ok {
			ac.Autopeer = parseBool(val)
		}
		if val, ok := section["autopeer_maxdepth"]; ok {
			i := parseInt(val)
			ac.AutopeerMaxdepth = &i
		}
		if val, ok := section["announce_interval"]; ok {
			i := parseInt(val) * 60
			ac.NodeAnnounceInterval = &i
		}
		if val, ok := section["message_storage_limit"]; ok {
			ac.MessageStorageLimit = parseFloat(val)
			if ac.MessageStorageLimit < 0.005 {
				ac.MessageStorageLimit = 0.005
			}
		}
		// Python checks both propagation_transfer_max_accepted_size and propagation_message_max_accepted_size
		if val, ok := section["propagation_transfer_max_accepted_size"]; ok {
			ac.PropagationTransferMaxAcceptedSize = parseFloat(val)
		}
		if val, ok := section["propagation_message_max_accepted_size"]; ok {
			ac.PropagationTransferMaxAcceptedSize = parseFloat(val)
		}
		if ac.PropagationTransferMaxAcceptedSize < 0.38 {
			ac.PropagationTransferMaxAcceptedSize = 0.38
		}

		if val, ok := section["propagation_sync_max_accepted_size"]; ok {
			ac.PropagationSyncMaxAcceptedSize = parseFloat(val)
			if ac.PropagationSyncMaxAcceptedSize < 0.38 {
				ac.PropagationSyncMaxAcceptedSize = 0.38
			}
		}

		if val, ok := section["propagation_stamp_cost_target"]; ok {
			ac.PropagationStampCostTarget = parseInt(val)
			if ac.PropagationStampCostTarget < 13 { // LXMF.LXMRouter.PROPAGATION_COST_MIN
				ac.PropagationStampCostTarget = 13
			}
		}
		if val, ok := section["propagation_stamp_cost_flexibility"]; ok {
			ac.PropagationStampCostFlexibility = parseInt(val)
			if ac.PropagationStampCostFlexibility < 0 {
				ac.PropagationStampCostFlexibility = 0
			}
		}
		if val, ok := section["peering_cost"]; ok {
			ac.PeeringCost = parseInt(val)
			if ac.PeeringCost < 0 {
				ac.PeeringCost = 0
			}
		}
		if val, ok := section["remote_peering_cost_max"]; ok {
			ac.RemotePeeringCostMax = parseInt(val)
			if ac.RemotePeeringCostMax < 0 {
				ac.RemotePeeringCostMax = 0
			}
		}
		if val, ok := section["prioritise_destinations"]; ok {
			ac.PrioritisedLXMFDestinations = parseList(val)
		}
		if val, ok := section["control_allowed"]; ok {
			ac.ControlAllowedIdentities = parseList(val)
		}
		if val, ok := section["static_peers"]; ok {
			peers := parseList(val)
			for _, p := range peers {
				if b, err := hex.DecodeString(p); err == nil {
					ac.StaticPeers = append(ac.StaticPeers, b)
				}
			}
		}
		if val, ok := section["max_peers"]; ok {
			i := parseInt(val)
			ac.MaxPeers = &i
		}
		if val, ok := section["from_static_only"]; ok {
			ac.FromStaticOnly = parseBool(val)
		}
	}

	// [logging]
	if section, ok := cfg["logging"]; ok {
		if val, ok := section["loglevel"]; ok {
			ac.LogLevel = parseInt(val)
		}
	}

	return ac, nil
}

func parseBool(s string) bool {
	s = strings.ToLower(s)
	return s == "yes" || s == "true" || s == "on" || s == "1"
}

func parseInt(s string) int {
	i, _ := strconv.Atoi(s)
	return i
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func loadConfig(configDir string) (*activeConfig, error) {
	configPath := filepath.Join(configDir, "config")
	var cfg map[string]map[string]string
	if isFile(configPath) {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		cfg = parseINI(string(data))
	} else {
		cfg = make(map[string]map[string]string)
	}

	ac, err := applyConfig(cfg)
	if err != nil {
		return nil, err
	}

	ignoredPath := filepath.Join(configDir, "ignored")
	if isFile(ignoredPath) {
		ac.IgnoredLXMFDestinations = loadHashList(ignoredPath)
	}

	allowedPath := filepath.Join(configDir, "allowed")
	if isFile(allowedPath) {
		ac.AllowedIdentities = loadHashList(allowedPath)
	}

	return ac, nil
}

func loadHashList(path string) [][]byte {
	var res [][]byte
	data, err := os.ReadFile(path)
	if err != nil {
		return res
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) == 16*2 { // RNS.Identity.TRUNCATED_HASHLENGTH//8*2 = 128//8*2 = 16*2 = 32
			if b, err := hex.DecodeString(line); err == nil {
				res = append(res, b)
			}
		}
	}
	return res
}

// parseINI parses a minimal INI/ConfigObj-style string.
func parseINI(data string) map[string]map[string]string {
	res := make(map[string]map[string]string)
	var currentSection string

	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)

		// Remove comments
		if idx := strings.IndexAny(line, "#;"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		if line == "" {
			continue
		}

		// Section: [name]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			if res[currentSection] == nil {
				res[currentSection] = make(map[string]string)
			}
			continue
		}

		// Key-Value: key = value
		if idx := strings.Index(line, "="); idx != -1 {
			key := strings.TrimSpace(line[:idx])
			value := strings.TrimSpace(line[idx+1:])
			if currentSection != "" {
				res[currentSection][key] = value
			}
		}
	}

	return res
}

// resolveConfigDir returns the directory where golxmd configuration is stored.
func resolveConfigDir(configDir string) string {
	home, _ := os.UserHomeDir()
	return resolveConfigDirCustom(configDir, home, "/etc/lxmd")
}

// resolveConfigDirCustom allows injecting paths for testing.
func resolveConfigDirCustom(configDir, userHome, etcDir string) string {
	if configDir != "" {
		return configDir
	}

	// Check /etc/lxmd
	etcConfig := filepath.Join(etcDir, "config")
	if isDir(etcDir) && isFile(etcConfig) {
		return etcDir
	}

	// Check ~/.config/lxmd
	userConfigDir := filepath.Join(userHome, ".config", "lxmd")
	userConfig := filepath.Join(userConfigDir, "config")
	if isDir(userConfigDir) && isFile(userConfig) {
		return userConfigDir
	}

	// Fallback to ~/.lxmd
	return filepath.Join(userHome, ".lxmd")
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
