// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"sync"
)

// InterfaceAnnouncer handles broadcasting interface availability.
type InterfaceAnnouncer struct {
	owner *Reticulum
	mu    sync.Mutex
}

// NewInterfaceAnnouncer creates a new InterfaceAnnouncer.
func NewInterfaceAnnouncer(owner *Reticulum) *InterfaceAnnouncer {
	return &InterfaceAnnouncer{
		owner: owner,
	}
}

// Start starts the announcer.
func (ia *InterfaceAnnouncer) Start() {
	// Minimal implementation without LXMF dependency
	Log("On-network interface discovery requires LXMF, which is not available in this Go port.", LogInfo, false)
}

// InterfaceDiscovery handles finding and connecting to interfaces.
type InterfaceDiscovery struct {
	owner *Reticulum
}

// NewInterfaceDiscovery creates a new InterfaceDiscovery.
func NewInterfaceDiscovery(owner *Reticulum) *InterfaceDiscovery {
	return &InterfaceDiscovery{
		owner: owner,
	}
}
