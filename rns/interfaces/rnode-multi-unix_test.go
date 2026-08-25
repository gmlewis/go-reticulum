// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux || darwin

package interfaces

import (
	"testing"
)

// TestNewRNodeMultiInterfaceMultipleEnabledSubinterfaces verifies that the
// multi-interface aggregator accepts multiple enabled sub-interfaces. With the
// Python-faithful construction policy, a child whose serial port is not
// available does NOT fail construction — it is registered offline with a
// background reconnect (mirroring RNodeInterface.__init__), so the multi is
// returned and must be detached to stop the child reconnect goroutines.
func TestNewRNodeMultiInterfaceMultipleEnabledSubinterfaces(t *testing.T) {
	t.Parallel()

	subs := []RNodeMultiSubinterfaceConfig{
		{Name: "sub0", Enabled: true, Frequency: 433050000, Bandwidth: 125000, TXPower: 10, SpreadingFactor: 7, CodingRate: 5},
		{Name: "sub1", Enabled: true, Frequency: 433150000, Bandwidth: 125000, TXPower: 10, SpreadingFactor: 7, CodingRate: 5},
	}

	iface, err := NewRNodeMultiInterface("rnode-multi", "/dev/ttyUSB0", 115200, 8, 1, "N", 0, "", subs, nil)
	if err != nil {
		t.Fatalf("constructor should not fail for an unavailable port (offline+reconnect): %v", err)
	}
	if iface == nil {
		t.Fatal("expected a non-nil multi interface")
	}
	// Both children are offline (no real device) but registered; detach to stop
	// their background reconnect goroutines.
	if err := iface.Detach(); err != nil {
		t.Fatalf("detach: %v", err)
	}
}

// TestNewRNodeMultiInterfaceNoEnabledSubinterfaces verifies the multi-interface
// still fails fast for a configuration error (no enabled sub-interfaces), which
// is a real config error rather than an unavailable port.
func TestNewRNodeMultiInterfaceNoEnabledSubinterfaces(t *testing.T) {
	t.Parallel()
	subs := []RNodeMultiSubinterfaceConfig{
		{Name: "sub0", Enabled: false, Frequency: 433050000, Bandwidth: 125000, TXPower: 10, SpreadingFactor: 7, CodingRate: 5},
	}
	iface, err := NewRNodeMultiInterface("rnode-multi", "/dev/ttyUSB0", 115200, 8, 1, "N", 0, "", subs, nil)
	if err == nil {
		_ = iface.Detach()
		t.Fatal("expected error when no sub-interfaces are enabled")
	}
}
