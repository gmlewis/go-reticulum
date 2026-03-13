// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package interfaces

import (
	"strings"
	"testing"
)

func TestNewRNodeMultiInterfaceMultipleEnabledSubinterfaces(t *testing.T) {
	t.Parallel()

	subs := []RNodeMultiSubinterfaceConfig{
		{Name: "sub0", Enabled: true, Frequency: 433050000, Bandwidth: 125000, TXPower: 10, SpreadingFactor: 7, CodingRate: 5},
		{Name: "sub1", Enabled: true, Frequency: 433150000, Bandwidth: 125000, TXPower: 10, SpreadingFactor: 7, CodingRate: 5},
	}

	iface, err := NewRNodeMultiInterface("rnode-multi", "/dev/ttyUSB0", 115200, 8, 1, "N", 0, "", subs, nil)
	if err == nil {
		t.Fatalf("expected constructor to fail without a real serial device")
	}
	if iface != nil {
		t.Fatalf("expected nil interface when constructor fails")
	}
	if strings.Contains(err.Error(), "exactly one enabled subinterface") {
		t.Fatalf("multiple-subinterface guard still active unexpectedly: %v", err)
	}
}
