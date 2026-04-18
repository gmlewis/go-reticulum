// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"fmt"
	"testing"
	"time"
)

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

func TestBaseInterfaceIFACKeyDerivation(t *testing.T) {
	bi := NewBaseInterface("ifac-test", ModeFull, 1000)
	cfg := IFACConfig{Enabled: true, NetName: "mesh", NetKey: "secret", Size: 16}
	bi.SetIFACConfig(cfg)

	// Expected value from Python script:
	// fb627f692fc06e22193bc67b5f38875b7e238e0b01dba3cc78da71f432012ce7702fd7d32af340d46c0c1bce096430133063d6362b3a54de341355424bfdbeb9
	expectedHex := "fb627f692fc06e22193bc67b5f38875b7e238e0b01dba3cc78da71f432012ce7702fd7d32af340d46c0c1bce096430133063d6362b3a54de341355424bfdbeb9"
	gotKey := fmt.Sprintf("%x", bi.ifacKey)
	if gotKey != expectedHex {
		t.Fatalf("IFAC key derivation mismatch:\ngot:  %v\nwant: %v", gotKey, expectedHex)
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

func TestBaseInterfaceDiscoveryRoundTrip(t *testing.T) {
	bi := NewBaseInterface("discovery-test", ModeFull, 1000)

	lat := 12.34
	lon := 56.78
	height := 90.12
	freq := 123456789
	bw := 250000
	wantLat := lat
	wantFreq := freq
	cfg := DiscoveryConfig{
		SupportsDiscovery: true,
		Discoverable:      true,
		AnnounceInterval:  6 * time.Hour,
		StampValue:        14,
		Name:              "Discovery Node",
		Encrypt:           true,
		ReachableOn:       "example.net",
		PublishIFAC:       true,
		Latitude:          &lat,
		Longitude:         &lon,
		Height:            &height,
		Frequency:         &freq,
		Bandwidth:         &bw,
		Modulation:        "lora",
	}

	bi.SetDiscoveryConfig(cfg)
	stored := bi.DiscoveryConfig()
	if !stored.SupportsDiscovery || !stored.Discoverable {
		t.Fatalf("unexpected discovery flags: %+v", stored)
	}
	if stored.AnnounceInterval != 6*time.Hour || stored.StampValue != 14 || stored.Name != "Discovery Node" {
		t.Fatalf("unexpected discovery config stored: %+v", stored)
	}
	if stored.ReachableOn != "example.net" || !stored.PublishIFAC || !stored.Encrypt || stored.Modulation != "lora" {
		t.Fatalf("unexpected discovery metadata stored: %+v", stored)
	}
	if stored.Latitude == nil || *stored.Latitude != lat || stored.Longitude == nil || *stored.Longitude != lon {
		t.Fatalf("unexpected discovery coordinates: %+v", stored)
	}
	if stored.Height == nil || *stored.Height != height || stored.Frequency == nil || *stored.Frequency != freq || stored.Bandwidth == nil || *stored.Bandwidth != bw {
		t.Fatalf("unexpected discovery radio values: %+v", stored)
	}

	*cfg.Latitude = 0
	*cfg.Frequency = 0
	if *stored.Latitude != wantLat || *stored.Frequency != wantFreq {
		t.Fatalf("stored discovery config should not alias caller pointers: %+v", stored)
	}
}
