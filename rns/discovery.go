// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"os"
	"path/filepath"
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

// DiscoveredInterface metadata constants
const (
	ThresholdUnknown = 24 * 60 * 60
	ThresholdStale   = 3 * 24 * 60 * 60
	ThresholdRemove  = 7 * 24 * 60 * 60

	StatusStale     = 0
	StatusUnknown   = 100
	StatusAvailable = 1000
)

// DiscoveredInterface represents metadata for a discovered interface.
type DiscoveredInterface struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Status      string   `json:"status"`
	StatusCode  int      `json:"status_code"`
	Hops        int      `json:"hops"`
	Discovered  float64  `json:"discovered"`
	LastHeard   float64  `json:"last_heard"`
	Transport   bool     `json:"transport"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
	Height      *float64 `json:"height"`
	Value       int      `json:"value"`
	ConfigEntry string   `json:"config_entry"`
	NetworkID   string   `json:"network_id,omitempty"`
	TransportID string   `json:"transport_id,omitempty"`
	ReachableOn string   `json:"reachable_on,omitempty"`
	Port        *int     `json:"port,omitempty"`
	Frequency   *int     `json:"frequency,omitempty"`
	Bandwidth   *int     `json:"bandwidth,omitempty"`
	SF          *int     `json:"sf,omitempty"`
	CR          *int     `json:"cr,omitempty"`
	Modulation  string   `json:"modulation,omitempty"`
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

		// Threshold for removal (7 days)
		if heardDelta > ThresholdRemove {
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

	return discovered, nil
}
