// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

func float64Ptr(v float64) *float64 { return &v }
func strPtr(v string) *string       { return &v }

func TestRenderInterfaceBasic(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:    "RNodeInterface[LoRa 915]",
		Status:  true,
		Mode:    modeAccessPoint,
		Bitrate: 1200,
		RXB:     15000,
		TXB:     8000,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	for _, want := range []string{
		" RNodeInterface[LoRa 915]\n",
		"    Status    : Up\n",
		"    Mode      : Access Point\n",
		"    Rate      : 1.20 kbps\n",
		"    Traffic   : ↑8.00 KB",
		"                ↓15.00 KB",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%v", want, got)
		}
	}
}

func TestRenderInterfaceDown(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:   "TCPInterface[Server on 0.0.0.0]",
		Status: false,
		Mode:   modeFull,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	if !strings.Contains(got, "    Status    : Down\n") {
		t.Errorf("expected Down status, got:\n%v", got)
	}
	if !strings.Contains(got, "    Mode      : Full\n") {
		t.Errorf("expected Full mode, got:\n%v", got)
	}
}

func TestRenderInterfaceSharedInstance(t *testing.T) {
	t.Parallel()
	clients := 3
	ifstat := rns.InterfaceStat{
		Name:    "Shared Instance[37428]",
		Status:  true,
		Mode:    modeFull,
		Clients: &clients,
		RXB:     1000000,
		TXB:     500000,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	if !strings.Contains(got, "    Serving   : 2 programs\n") {
		t.Errorf("expected serving line, got:\n%v", got)
	}
	if strings.Contains(got, "    Mode") {
		t.Errorf("Shared Instance should not show Mode, got:\n%v", got)
	}
}

func TestRenderInterfaceNoiseFloor(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:         "RNodeInterface[LoRa 915]",
		Status:       true,
		Mode:         modeFull,
		NoiseFloor:   float64Ptr(-119.0),
		Interference: float64Ptr(-95.0),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	if !strings.Contains(got, "    Noise Fl. : -119 dBm") {
		t.Errorf("expected noise floor, got:\n%v", got)
	}
	if !strings.Contains(got, "    Intrfrnc. : -95 dBm") {
		t.Errorf("expected interference, got:\n%v", got)
	}
}

func TestRenderInterfaceNoiseFloorNoInterference(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:         "RNodeInterface[LoRa 915]",
		Status:       true,
		Mode:         modeFull,
		NoiseFloor:   float64Ptr(-119.0),
		Interference: float64Ptr(0),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	if !strings.Contains(got, "    Noise Fl. : -119 dBm, no interference") {
		t.Errorf("expected no interference, got:\n%v", got)
	}
}

func TestRenderInterfaceCPU(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:    "RNodeInterface[LoRa 915]",
		Status:  true,
		Mode:    modeFull,
		CPULoad: float64Ptr(45.2),
		CPUTemp: float64Ptr(62.5),
		MemLoad: float64Ptr(38.1),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	for _, want := range []string{
		"    CPU load  : 45.2 %\n",
		"    CPU temp  : 62.5°C\n",
		"    Mem usage : 38.1 %\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%v", want, got)
		}
	}
}

func TestRenderInterfaceBattery(t *testing.T) {
	t.Parallel()
	bp := 85
	ifstat := rns.InterfaceStat{
		Name:           "RNodeInterface[LoRa 915]",
		Status:         true,
		Mode:           modeFull,
		BatteryPercent: &bp,
		BatteryState:   "charging",
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	if !strings.Contains(got, "    Battery   : 85% (charging)\n") {
		t.Errorf("expected battery line, got:\n%v", got)
	}
}

func TestRenderInterfaceAirtime(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:            "RNodeInterface[LoRa 915]",
		Status:          true,
		Mode:            modeFull,
		AirtimeShort:    float64Ptr(1.5),
		AirtimeLong:     float64Ptr(0.8),
		ChannelLoadShrt: float64Ptr(2.3),
		ChannelLoadLong: float64Ptr(1.1),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	for _, want := range []string{
		"    Airtime   : 1.5% (15s), 0.8% (1h)\n",
		"    Ch. Load  : 2.3% (15s), 1.1% (1h)\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%v", want, got)
		}
	}
}

func TestRenderInterfaceSwitchEndpoint(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:        "RNodeInterface[LoRa 915]",
		Status:      true,
		Mode:        modeFull,
		SwitchID:    strPtr("abc123"),
		EndpointID:  strPtr("def456"),
		ViaSwitchID: strPtr("ghi789"),
		Peers:       intPtr(5),
		TunnelState: strPtr("Connected"),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	for _, want := range []string{
		"    Switch ID : abc123\n",
		"    Endpoint  : def456\n",
		"    Via       : ghi789\n",
		"    Peers     : 5 reachable\n",
		"    I2P       : Connected\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%v", want, got)
		}
	}
}

func TestRenderInterfaceIFAC(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:          "RNodeInterface[LoRa 915]",
		Status:        true,
		Mode:          modeFull,
		IFACSignature: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a},
		IFACSize:      2,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	if !strings.Contains(got, "    Access    : 16-bit IFAC by <…060708090a>\n") {
		t.Errorf("expected IFAC line, got:\n%v", got)
	}
}

func TestRenderInterfaceI2PB32(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:   "I2PInterface[test]",
		Status: true,
		Mode:   modeFull,
		I2PB32: strPtr("abc123.b32.i2p"),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	if !strings.Contains(got, "    I2P B32   : abc123.b32.i2p\n") {
		t.Errorf("expected I2P B32 line, got:\n%v", got)
	}
}

func TestRenderInterfaceAnnounceStats(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:            "RNodeInterface[LoRa 915]",
		Status:          true,
		Mode:            modeFull,
		AnnounceQueue:   intPtr(3),
		HeldAnnounces:   intPtr(1),
		InAnnounceFreq:  float64Ptr(0.5),
		OutAnnounceFreq: float64Ptr(1.2),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, true)
	got := buf.String()

	for _, want := range []string{
		"    Queued    : 3 announces\n",
		"    Held      : 1 announce\n",
		"    Announces : ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%v", want, got)
		}
	}
}

func TestRenderInterfaceAnnounceStatsNotShownWithoutFlag(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:            "RNodeInterface[LoRa 915]",
		Status:          true,
		Mode:            modeFull,
		AnnounceQueue:   intPtr(3),
		HeldAnnounces:   intPtr(1),
		InAnnounceFreq:  float64Ptr(0.5),
		OutAnnounceFreq: float64Ptr(1.2),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	for _, notWant := range []string{
		"Queued",
		"Held",
		"Announces",
	} {
		if strings.Contains(got, notWant) {
			t.Errorf("output should not contain %q without astats, got:\n%v", notWant, got)
		}
	}
}

func TestRenderTrafficWithSpeed(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		RXB: 1500000,
		TXB: 800000,
		RXS: 1200,
		TXS: 600,
	}
	var buf bytes.Buffer
	renderTraffic(&buf, ifstat)
	got := buf.String()

	if !strings.Contains(got, "    Traffic   : ↑") {
		t.Errorf("expected traffic header, got:\n%v", got)
	}
	if !strings.Contains(got, "↓") {
		t.Errorf("expected rx traffic, got:\n%v", got)
	}
}

func TestRenderInterfaceAutoconnect(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:              "RNodeInterface[LoRa 915]",
		Status:            true,
		Mode:              modeFull,
		AutoconnectSource: "ble_scanner",
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	if !strings.Contains(got, "    Source    : Auto-connect via <ble_scanner>\n") {
		t.Errorf("expected autoconnect source, got:\n%v", got)
	}
}

func TestLinkStatsString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		linkCount      *int
		hasTransportID bool
		want           string
	}{
		{"nil", nil, false, ""},
		{"nil with transport", nil, true, ""},
		{"1 entry no transport", intPtr(1), false, " 1 entry in link table"},
		{"1 entry with transport", intPtr(1), true, ", 1 entry in link table"},
		{"5 entries no transport", intPtr(5), false, " 5 entries in link table"},
		{"5 entries with transport", intPtr(5), true, ", 5 entries in link table"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := linkStatsString(tc.linkCount, tc.hasTransportID)
			if got != tc.want {
				t.Errorf("linkStatsString(%v, %v) = %q, want %q",
					tc.linkCount, tc.hasTransportID, got, tc.want)
			}
		})
	}
}

func TestRenderTotals(t *testing.T) {
	t.Parallel()
	stats := &rns.InterfaceStatsSnapshot{
		RXB: 5000000,
		TXB: 2000000,
		RXS: 1200,
		TXS: 800,
	}
	var buf bytes.Buffer
	renderTotals(&buf, stats)
	got := buf.String()

	for _, want := range []string{
		"\n Totals       : ↑",
		"\n                ↓",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%v", want, got)
		}
	}
}

func TestRenderTransportFooter(t *testing.T) {
	t.Parallel()
	uptime := 3661.0
	stats := &rns.InterfaceStatsSnapshot{
		TransportID:     []byte{0xab, 0xcd, 0xef, 0x01},
		NetworkID:       []byte{0x12, 0x34, 0x56},
		ProbeResponder:  []byte{0xaa, 0xbb},
		TransportUptime: &uptime,
	}
	var buf bytes.Buffer
	renderTransportFooter(&buf, stats, ", 5 entries in link table")
	got := buf.String()

	for _, want := range []string{
		"Transport Instance <abcdef01> running",
		"Network Identity   <123456>",
		"Probe responder at <aabb> active",
		"Uptime is 1h, 1m and 1s, 5 entries in link table",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%v", want, got)
		}
	}
}

func TestRenderTransportFooterNoTransport(t *testing.T) {
	t.Parallel()
	stats := &rns.InterfaceStatsSnapshot{}
	var buf bytes.Buffer
	renderTransportFooter(&buf, stats, " 3 entries in link table")
	got := buf.String()

	if !strings.Contains(got, " 3 entries in link table") {
		t.Errorf("expected link table line, got:\n%v", got)
	}
}

func TestRenderTransportFooterNoTransportNoLinks(t *testing.T) {
	t.Parallel()
	stats := &rns.InterfaceStatsSnapshot{}
	var buf bytes.Buffer
	renderTransportFooter(&buf, stats, "")
	got := buf.String()

	if got != "" {
		t.Errorf("expected empty output, got:\n%v", got)
	}
}

func TestRenderDiscoveredInterfaces(t *testing.T) {
	t.Parallel()
	now := float64(time.Now().UnixNano()) / 1e9
	ifs := []rns.DiscoveredInterface{
		{
			Name:      "Test UDP",
			Type:      "UDPInterface",
			Status:    "available",
			LastHeard: now - 30,
			Value:     100,
			Latitude:  float64Ptr(34.0522),
			Longitude: float64Ptr(-118.2437),
		},
		{
			Name:      "Stale RNode",
			Type:      "RNodeInterface",
			Status:    "stale",
			LastHeard: now - 400000,
			Value:     50,
		},
	}

	var buf bytes.Buffer
	renderDiscoveredInterfaces(&buf, ifs)
	got := buf.String()

	// Check table headers and content
	for _, want := range []string{
		"Name                      Type         Status       Last Heard   Value    Location       ",
		"-----------------------------------------------------------------------------------------",
		"Test UDP                  UDP          ✓ Available  Just now     100      34.0522, -118.2437",
		"Stale RNode               RNode        × Stale      4d ago       50       N/A            ",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%v", want, got)
		}
	}
}

func TestRenderDiscoveredInterfaceDetails(t *testing.T) {
	t.Parallel()
	now := float64(time.Now().UnixNano()) / 1e9
	ifs := []rns.DiscoveredInterface{
		{
			Name:        "Detailed Interface",
			Type:        "RNodeInterface",
			Status:      "available",
			Transport:   true,
			Hops:        1,
			Discovered:  now - 3600,
			LastHeard:   now - 600,
			Latitude:    float64Ptr(1.2345),
			Longitude:   float64Ptr(6.7890),
			Height:      float64Ptr(150),
			Frequency:   intPtr(915000000),
			Bandwidth:   intPtr(125000),
			SF:          intPtr(7),
			CR:          intPtr(5),
			Value:       500,
			ConfigEntry: "[[Detailed Interface]]\n  type = RNodeInterface\n  port = /dev/ttyUSB0",
		},
	}

	var buf bytes.Buffer
	renderDiscoveredInterfaceDetails(&buf, ifs)
	got := buf.String()

	for _, want := range []string{
		"Name         : Detailed Interface",
		"Type         : RNodeInterface",
		"Status       : Available",
		"Transport    : Enabled",
		"Distance     : 1 hop",
		"Location     : 1.2345, 6.7890, 150m h",
		"Frequency    : 915,000,000 Hz",
		"Bandwidth    : 125,000 Hz",
		"Sprd. Factor : 7",
		"Coding Rate  : 5",
		"Stamp Value  : 500",
		"Configuration Entry:",
		"  [[Detailed Interface]]",
		"  type = RNodeInterface",
		"  port = /dev/ttyUSB0",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\ngot:\n%v", want, got)
		}
	}
}

func TestRenderInterfaceNetwork(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:        "RNodeInterface[LoRa 915]",
		Status:      true,
		Mode:        modeFull,
		IFACNetname: "mynetwork",
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false)
	got := buf.String()

	if !strings.Contains(got, "    Network   : mynetwork\n") {
		t.Errorf("expected network name, got:\n%v", got)
	}
}
