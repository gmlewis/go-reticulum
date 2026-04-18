// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// InterfaceAnnouncer manages the periodic broadcast of local interface availability to dynamically discoverable peers on the network.
type InterfaceAnnouncer struct {
	logger *Logger
	owner  *Reticulum
}

// NewInterfaceAnnouncer initializes a new announcer component bound to the provided local Reticulum instance.
func NewInterfaceAnnouncer(owner *Reticulum, logger *Logger) *InterfaceAnnouncer {
	return &InterfaceAnnouncer{
		logger: logger,
		owner:  owner,
	}
}

// Start triggers the underlying background mechanism that begins transmitting interface presence announcements.
func (ia *InterfaceAnnouncer) Start() {
	// Minimal implementation without LXMF dependency
	ia.logger.Info("On-network interface discovery requires LXMF, which is not available in this Go port.")
}

// Discovered-interface age thresholds and status values.
const (
	// ThresholdUnknown is the age in seconds after which an interface is marked
	// unknown.
	ThresholdUnknown = 24 * 60 * 60
	// ThresholdStale is the age in seconds after which an interface is marked
	// stale.
	ThresholdStale = 3 * 24 * 60 * 60
	// ThresholdRemove is the age in seconds after which cached discovery data is
	// removed.
	ThresholdRemove = 7 * 24 * 60 * 60

	// StatusStale marks an interface as stale.
	StatusStale = 0
	// StatusUnknown marks an interface as unknown.
	StatusUnknown = 100
	// StatusAvailable marks an interface as available.
	StatusAvailable = 1000
)

// DiscoveredInterface describes one interface discovered from cached announce
// data.
type DiscoveredInterface struct {
	// Name is the discovered interface name.
	Name string `json:"name"`
	// Type is the discovered interface type.
	Type string `json:"type"`
	// Status is the human-readable availability state.
	Status string `json:"status"`
	// StatusCode is the numeric availability state.
	StatusCode int `json:"status_code"`
	// Hops is the reported hop count to the interface.
	Hops int `json:"hops"`
	// Discovered is the Unix timestamp when the interface was first recorded.
	Discovered float64 `json:"discovered"`
	// LastHeard is the Unix timestamp of the latest discovery update.
	LastHeard float64 `json:"last_heard"`
	// Transport reports whether the interface acts as a transport node.
	Transport bool     `json:"transport"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Height    *float64 `json:"height"`
	// Value is the discovery ranking value carried in the cache entry.
	Value int `json:"value"`
	// ConfigEntry is a generated config snippet for recreating the interface.
	ConfigEntry string `json:"config_entry"`
	NetworkID   string `json:"network_id,omitempty"`
	TransportID string `json:"transport_id,omitempty"`
	ReachableOn string `json:"reachable_on,omitempty"`
	Port        *int   `json:"port,omitempty"`
	Frequency   *int   `json:"frequency,omitempty"`
	Bandwidth   *int   `json:"bandwidth,omitempty"`
	SF          *int   `json:"sf,omitempty"`
	CR          *int   `json:"cr,omitempty"`
	Modulation  string `json:"modulation,omitempty"`
}

// InterfaceDiscovery actively listens for and processes inbound presence announcements from remote nodes to establish automatic connections.
type InterfaceDiscovery struct {
	owner *Reticulum
}

// NewInterfaceDiscovery initializes a discovery listener bound to the provided local Reticulum configuration.
func NewInterfaceDiscovery(owner *Reticulum) *InterfaceDiscovery {
	return &InterfaceDiscovery{
		owner: owner,
	}
}

func (id *InterfaceDiscovery) persistDiscoveredInterface(info map[string]any) error {
	if id.owner == nil {
		return fmt.Errorf("no Reticulum instance")
	}

	discoveryHash, err := discoveryHashFilename(info["discovery_hash"])
	if err != nil {
		return err
	}

	receivedAt := asFloat64(info["received"])
	if receivedAt == 0 {
		return fmt.Errorf("missing received timestamp")
	}

	storagePath := filepath.Join(id.owner.configDir, "discovery", "interfaces")
	if err := os.MkdirAll(storagePath, 0o755); err != nil {
		return err
	}

	filePath := filepath.Join(storagePath, discoveryHash+".data")
	persisted := cloneStringAnyMap(info)
	persisted["last_heard"] = receivedAt

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		persisted["discovered"] = receivedAt
		persisted["heard_count"] = 0
		data, err := msgpack.Pack(persisted)
		if err != nil {
			return err
		}
		return os.WriteFile(filePath, data, 0o644)
	} else if err != nil {
		return err
	}

	lastData, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	unpacked, err := msgpack.Unpack(lastData)
	if err != nil {
		return err
	}
	lastInfo := asAnyMap(unpacked)
	if lastInfo == nil {
		return fmt.Errorf("unexpected discovery cache type %T", unpacked)
	}

	discoveredAt := asFloat64(lookupAnyValue(lastInfo, "discovered"))
	if discoveredAt == 0 {
		discoveredAt = receivedAt
	}
	persisted["discovered"] = discoveredAt
	persisted["heard_count"] = asInt(lookupAnyValue(lastInfo, "heard_count")) + 1

	data, err := msgpack.Pack(persisted)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0o644)
}

// ListDiscoveredInterfaces returns a list of discovered interfaces.
func (id *InterfaceDiscovery) ListDiscoveredInterfaces(onlyAvailable, onlyTransport bool) ([]DiscoveredInterface, error) {
	if id.owner == nil {
		return nil, fmt.Errorf("no Reticulum instance")
	}

	storagePath := filepath.Join(id.owner.configDir, "discovery", "interfaces")
	if _, err := os.Stat(storagePath); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(storagePath)
	if err != nil {
		return nil, err
	}

	now := float64(time.Now().UnixNano()) / 1e9
	discoverySources := id.owner.interfaceSources
	var discovered []DiscoveredInterface

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(storagePath, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			id.owner.logger.Warning("failed to read discovery file %q: %v", path, err)
			continue
		}

		unpacked, err := msgpack.Unpack(data)
		if err != nil {
			id.owner.logger.Warning("failed to unpack discovery file %q: %v", path, err)
			continue
		}
		m, ok := unpacked.(map[any]any)
		if !ok {
			continue
		}

		heardAt := asFloat64(lookupAnyValue(m, "last_heard"))
		heardDelta := now - heardAt

		shouldRemove := false
		if heardDelta > ThresholdRemove {
			shouldRemove = true
		} else if len(discoverySources) > 0 {
			networkIDHex := asString(lookupAnyValue(m, "network_id"))
			if networkIDHex == "" {
				shouldRemove = true
			} else {
				networkID, err := hex.DecodeString(networkIDHex)
				if err != nil || !hasDiscoverySource(discoverySources, networkID) {
					shouldRemove = true
				}
			}
		}
		if !shouldRemove {
			reachableOn := asString(lookupAnyValue(m, "reachable_on"))
			if reachableOn != "" && !isReachableOnValue(reachableOn) {
				shouldRemove = true
			}
		}

		if shouldRemove {
			if err := os.Remove(path); err != nil {
				id.owner.logger.Warning("failed to remove expired discovery file %q: %v", path, err)
			}
			continue
		}

		// Status calculation
		status := "available"
		statusCode := StatusAvailable
		if heardDelta > ThresholdStale {
			status = "stale"
			statusCode = StatusStale
		} else if heardDelta > ThresholdUnknown {
			status = "unknown"
			statusCode = StatusUnknown
		}

		if onlyAvailable && status != "available" {
			continue
		}

		transport := asBool(lookupAnyValue(m, "transport"))
		if onlyTransport && !transport {
			continue
		}

		di := DiscoveredInterface{
			Name:        asString(lookupAnyValue(m, "name")),
			Type:        asString(lookupAnyValue(m, "type")),
			Status:      status,
			StatusCode:  statusCode,
			Hops:        asInt(lookupAnyValue(m, "hops")),
			Discovered:  asFloat64(lookupAnyValue(m, "discovered")),
			LastHeard:   heardAt,
			Transport:   transport,
			Value:       asInt(lookupAnyValue(m, "value")),
			ConfigEntry: asString(lookupAnyValue(m, "config_entry")),
			NetworkID:   asString(lookupAnyValue(m, "network_id")),
			TransportID: asString(lookupAnyValue(m, "transport_id")),
			ReachableOn: asString(lookupAnyValue(m, "reachable_on")),
			Modulation:  asString(lookupAnyValue(m, "modulation")),
		}

		di.Latitude = lookupOptFloat64(m, "latitude")
		di.Longitude = lookupOptFloat64(m, "longitude")
		di.Height = lookupOptFloat64(m, "height")
		di.Port = lookupOptInt(m, "port")
		di.Frequency = lookupOptInt(m, "frequency")
		di.Bandwidth = lookupOptInt(m, "bandwidth")
		di.SF = lookupOptInt(m, "sf")
		di.CR = lookupOptInt(m, "cr")

		discovered = append(discovered, di)
	}

	sort.Slice(discovered, func(i, j int) bool {
		left := discovered[i]
		right := discovered[j]
		if left.StatusCode != right.StatusCode {
			return left.StatusCode > right.StatusCode
		}
		if left.Value != right.Value {
			return left.Value > right.Value
		}
		return left.LastHeard > right.LastHeard
	})

	return discovered, nil
}

func hasDiscoverySource(sources [][]byte, networkID []byte) bool {
	for _, source := range sources {
		if bytes.Equal(source, networkID) {
			return true
		}
	}
	return false
}

func isReachableOnValue(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if net.ParseIP(v) != nil {
		return true
	}
	return isHostname(v)
}

func isHostname(v string) bool {
	if len(v) == 0 || len(v) > 253 {
		return false
	}
	labels := strings.Split(v, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func discoveryHashFilename(v any) (string, error) {
	switch t := v.(type) {
	case []byte:
		if len(t) == 0 {
			return "", fmt.Errorf("missing discovery hash")
		}
		return hex.EncodeToString(t), nil
	case string:
		if t == "" {
			return "", fmt.Errorf("missing discovery hash")
		}
		return t, nil
	default:
		return "", fmt.Errorf("unsupported discovery hash type %T", v)
	}
}

func cloneStringAnyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
