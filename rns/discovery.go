// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

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
