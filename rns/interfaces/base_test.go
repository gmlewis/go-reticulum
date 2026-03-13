// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import "testing"

func TestBaseInterfaceIFACRoundTrip(t *testing.T) {
	bi := NewBaseInterface("ifac-test", ModeFull, 1000)

	cfg := IFACConfig{Enabled: true, NetName: "mesh", NetKey: "secret", Size: 16}
	bi.SetIFACConfig(cfg)

	stored := bi.IFACConfig()
	if stored.NetName != "mesh" || stored.NetKey != "secret" || stored.Size != 16 || !stored.Enabled {
		t.Fatalf("unexpected IFAC config stored: %+v", stored)
	}

	raw := []byte{0x11, 0x22, 0x33, 0x44, 0x55}
	outProcessed, err := bi.ApplyIFACOutbound(raw)
	if err != nil {
		t.Fatalf("unexpected outbound IFAC error: %v", err)
	}
	if len(outProcessed) != len(raw)+stored.Size {
		t.Fatalf("outbound IFAC length mismatch: got %v want %v", len(outProcessed), len(raw)+stored.Size)
	}
	if outProcessed[0]&0x80 == 0 {
		t.Fatalf("expected IFAC flag to be set in outbound frame")
	}

	inProcessed, ok := bi.ApplyIFACInbound(outProcessed)
	if !ok {
		t.Fatalf("expected inbound IFAC verification to accept packet")
	}
	if string(inProcessed) != string(raw) {
		t.Fatalf("inbound IFAC round-trip mismatch")
	}
}

func TestBaseInterfaceIFACDropsWhenEnabledButFlagMissing(t *testing.T) {
	bi := NewBaseInterface("ifac-test", ModeFull, 1000)
	bi.SetIFACConfig(IFACConfig{Enabled: true, NetName: "mesh", NetKey: "secret", Size: 16})

	if _, ok := bi.ApplyIFACInbound([]byte{0x01, 0x02, 0x03, 0x04}); ok {
		t.Fatalf("expected inbound drop when IFAC is enabled but packet lacks IFAC flag")
	}
}

func TestBaseInterfaceIFACDropsFlaggedWhenDisabled(t *testing.T) {
	bi := NewBaseInterface("ifac-test", ModeFull, 1000)
	bi.SetIFACConfig(IFACConfig{Enabled: false})

	flagged := []byte{0x80, 0x01, 0x02, 0x03}
	if _, ok := bi.ApplyIFACInbound(flagged); ok {
		t.Fatalf("expected inbound drop when IFAC flag is set on non-IFAC interface")
	}
}
