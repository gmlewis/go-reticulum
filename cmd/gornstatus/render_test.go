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
	renderInterface(&buf, ifstat, false, false)
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
	renderInterface(&buf, ifstat, false, false)
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
	renderInterface(&buf, ifstat, false, false)
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
		NoiseFloor:   new(-119.0),
		Interference: new(-95.0),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
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
		NoiseFloor:   new(-119.0),
		Interference: new(float64(0)),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
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
		CPULoad: new(45.2),
		CPUTemp: new(62.5),
		MemLoad: new(38.1),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
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
	renderInterface(&buf, ifstat, false, false)
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
		AirtimeShort:    new(1.5),
		AirtimeLong:     new(0.8),
		ChannelLoadShrt: new(2.3),
		ChannelLoadLong: new(1.1),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
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
		SwitchID:    new("abc123"),
		EndpointID:  new("def456"),
		ViaSwitchID: new("ghi789"),
		Peers:       new(5),
		TunnelState: new("Connected"),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
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
	renderInterface(&buf, ifstat, false, false)
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
		I2PB32: new("abc123.b32.i2p"),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
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
		AnnounceQueue:   new(3),
		HeldAnnounces:   new(1),
		InAnnounceFreq:  new(0.5),
		OutAnnounceFreq: new(1.2),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, true, false)
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
		AnnounceQueue:   new(3),
		HeldAnnounces:   new(1),
		InAnnounceFreq:  new(0.5),
		OutAnnounceFreq: new(1.2),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
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
		Name:   "RNodeInterface[LoRa 915]",
		Status: true,
		Mode:   modeFull,
		RXB:    1500000,
		TXB:    800000,
		RXS:    1200,
		TXS:    600,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
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
	renderInterface(&buf, ifstat, false, false)
	got := buf.String()

	if !strings.Contains(got, "    Source    : Auto-connect via <ble_scanner>\n") {
		t.Errorf("expected autoconnect source, got:\n%v", got)
	}
}

// renderBlock renders ifstat and returns the trailing Announces / Path
// Rqs. / Traffic block, stripping the per-interface header so it can be
// compared byte-for-byte against the Python rnstatus.py golden output.
func renderBlock(ifstat rns.InterfaceStat, astats bool, pstats bool) string {
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, astats, pstats)
	s := buf.String()
	markers := []string{"    Path Rqs. : ", "    Announces : ", "    Traffic   : "}
	best := len(s)
	for _, m := range markers {
		if i := strings.Index(s, m); i >= 0 && i < best {
			best = i
		}
	}
	return s[best:]
}

// TestRenderInterfacePythonParity asserts the Announces / Path Rqs. /
// Traffic block matches the Python rnstatus.py (v1.4.2) output
// byte-for-byte for a matrix of (astats, pstats) and field combinations.
// The expected output for every case is captured live from the installed
// RNS by running the verbatim rnstatus.py:564-644 display block (via
// pythonRenderBlocks); there are no embedded golden strings.
func TestRenderInterfacePythonParity(t *testing.T) {
	t.Parallel()
	clients2 := 2
	peers4 := 4
	cases := []struct {
		name   string
		ifstat rns.InterfaceStat
		astats bool
		pstats bool
	}{
		{
			"astats+pstats no bursts",
			rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]", Status: true, Mode: modeFull,
				InAnnounceFreq: new(0.5), OutAnnounceFreq: new(1.2),
				InPrFreq: new(0.5), OutPrFreq: new(2.0)},
			true, true,
		},
		{
			"pstats only suffix-arrow",
			rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]", Status: true, Mode: modeFull,
				InPrFreq: new(0.5), OutPrFreq: new(2.0)},
			false, true,
		},
		{
			"per-client /c rate",
			rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]", Status: true, Mode: modeFull,
				Clients: &clients2, InAnnounceFreq: new(1.0), OutAnnounceFreq: new(4.0)},
			true, false,
		},
		{
			"per-peer /p rate (AutoInterface)",
			rns.InterfaceStat{Name: "AutoInterface[test]", Status: true, Mode: modeAccessPoint,
				Peers: &peers4, InAnnounceFreq: new(1.0), OutAnnounceFreq: new(4.0)},
			true, false,
		},
		{
			"announce-rate target/penalty/grace",
			rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]", Status: true, Mode: modeFull,
				InAnnounceFreq: new(0.5), OutAnnounceFreq: new(1.2),
				AnnounceRateTarget: new(30), AnnounceRatePenalty: new(10), AnnounceRateGrace: new(5)},
			true, false,
		},
		{
			"traffic with speed",
			rns.InterfaceStat{Name: "RNodeInterface[LoRa 915]", Status: true, Mode: modeFull,
				RXB: 1500000, TXB: 800000, RXS: 9600, TXS: 4800},
			false, false,
		},
	}
	pyCases := make([]pyRenderBlockCase, len(cases))
	for i, tc := range cases {
		pyCases[i] = pyRenderBlockCase{
			Name:    tc.ifstat.Name,
			Mode:    tc.ifstat.Mode,
			Status:  tc.ifstat.Status,
			Clients: tc.ifstat.Clients,
			Peers:   tc.ifstat.Peers,
			InAnn:   tc.ifstat.InAnnounceFreq,
			OutAnn:  tc.ifstat.OutAnnounceFreq,
			InPr:    tc.ifstat.InPrFreq,
			OutPr:   tc.ifstat.OutPrFreq,
			Art:     tc.ifstat.AnnounceRateTarget,
			Arp:     tc.ifstat.AnnounceRatePenalty,
			Arg:     tc.ifstat.AnnounceRateGrace,
			RXB:     float64(tc.ifstat.RXB),
			TXB:     float64(tc.ifstat.TXB),
			RXS:     tc.ifstat.RXS,
			TXS:     tc.ifstat.TXS,
			Astats:  tc.astats,
			Pstats:  tc.pstats,
		}
	}
	wants := pythonRenderBlocks(t, pyCases)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := renderBlock(tc.ifstat, tc.astats, tc.pstats)
			if got != wants[i] {
				t.Errorf("parity mismatch for %q vs live Python:\n got: %q\nwant: %q", tc.name, got, wants[i])
			}
		})
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
		{"1 entry no transport", new(1), false, " 1 entry in link table"},
		{"1 entry with transport", new(1), true, ", 1 entry in link table"},
		{"5 entries no transport", new(5), false, " 5 entries in link table"},
		{"5 entries with transport", new(5), true, ", 5 entries in link table"},
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
			Latitude:  new(34.0522),
			Longitude: new(-118.2437),
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
			Latitude:    new(1.2345),
			Longitude:   new(6.7890),
			Height:      new(float64(150)),
			Frequency:   new(915000000),
			Bandwidth:   new(125000),
			SF:          new(7),
			CR:          new(5),
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
	renderInterface(&buf, ifstat, false, false)
	got := buf.String()

	if !strings.Contains(got, "    Network   : mynetwork\n") {
		t.Errorf("expected network name, got:\n%v", got)
	}
}

// TestRenderInterfaceGravity verifies that a nonzero interface gravity is
// annotated on the Status line as ", gravity X" (Python rnstatus.py:423,
// v1.4.1: `if "gravity" in ifstat and ifstat["gravity"]: ss += ", gravity
// "+str(ifstat["gravity"])`). Zero gravity (Python falsy) must not annotate.
func TestRenderInterfaceGravity(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:    "RNodeInterface[LoRa 915]",
		Status:  true,
		Mode:    modeAccessPoint,
		Bitrate: 1200,
		Gravity: 7,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
	got := buf.String()
	if !strings.Contains(got, "    Status    : Up, gravity 7\n") {
		t.Errorf("expected status line with gravity annotation, got:\n%v", got)
	}

	// Zero gravity is falsy in Python and must not annotate the status line.
	var buf2 bytes.Buffer
	ifstat.Gravity = 0
	renderInterface(&buf2, ifstat, false, false)
	if !strings.Contains(buf2.String(), "    Status    : Up\n") {
		t.Errorf("expected plain Up status for zero gravity, got:\n%v", buf2.String())
	}
	if strings.Contains(buf2.String(), "gravity") {
		t.Errorf("zero gravity should not annotate status, got:\n%v", buf2.String())
	}

	// Down + gravity still annotates.
	var buf3 bytes.Buffer
	ifstat.Gravity = 3
	ifstat.Status = false
	renderInterface(&buf3, ifstat, false, false)
	if !strings.Contains(buf3.String(), "    Status    : Down, gravity 3\n") {
		t.Errorf("expected Down status with gravity annotation, got:\n%v", buf3.String())
	}
}

// TestRenderInterfacePrStats verifies the -P/--pr-stats Path Rqs. block
// (Python rnstatus.py:628-632) when announce stats are NOT also enabled:
// the arrow is suffixed onto the frequency ("X.X Hz↑"/"X.X Hz↓").
func TestRenderInterfacePrStats(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:      "RNodeInterface[LoRa 915]",
		Status:    true,
		Mode:      modeFull,
		InPrFreq:  new(0.5),
		OutPrFreq: new(2.0),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, true)
	got := buf.String()

	if !strings.Contains(got, "    Path Rqs. : 2.0 Hz↑") {
		t.Errorf("expected Path Rqs. outgoing line, got:\n%v", got)
	}
	if !strings.Contains(got, "                0.5 Hz↓") {
		t.Errorf("expected Path Rqs. incoming line, got:\n%v", got)
	}
	if strings.Contains(got, "Announces") {
		t.Errorf("Announces block must not render without astats, got:\n%v", got)
	}
}

// TestRenderInterfacePrStatsWithAnnounce verifies that with both -A and
// -P the arrows are prefixed ("↑X.X Hz") and both blocks render
// (Python rnstatus.py:618-626).
func TestRenderInterfacePrStatsWithAnnounce(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:            "RNodeInterface[LoRa 915]",
		Status:          true,
		Mode:            modeFull,
		InAnnounceFreq:  new(0.5),
		OutAnnounceFreq: new(1.2),
		InPrFreq:        new(0.5),
		OutPrFreq:       new(2.0),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, true, true)
	got := buf.String()

	if !strings.Contains(got, "    Path Rqs. : ↑2.0 Hz") {
		t.Errorf("expected Path Rqs. prefixed-arrow outgoing line, got:\n%v", got)
	}
	if !strings.Contains(got, "                ↓0.5 Hz") {
		t.Errorf("expected Path Rqs. prefixed-arrow incoming line, got:\n%v", got)
	}
	if !strings.Contains(got, "    Announces : 1.2 Hz↑") {
		t.Errorf("expected Announces outgoing line, got:\n%v", got)
	}
}

// TestRenderInterfaceBurstStatus verifies the burst-for annotations
// (Python rnstatus.py:567-574,630,635).
func TestRenderInterfaceBurstStatus(t *testing.T) {
	t.Parallel()
	now := float64(time.Now().UnixNano()) / 1e9
	ifstat := rns.InterfaceStat{
		Name:             "RNodeInterface[LoRa 915]",
		Status:           true,
		Mode:             modeFull,
		InAnnounceFreq:   new(0.5),
		OutAnnounceFreq:  new(1.2),
		InPrFreq:         new(0.5),
		OutPrFreq:        new(2.0),
		BurstActive:      true,
		BurstActivated:   now - 60,
		PrBurstActive:    true,
		PrBurstActivated: now - 60,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, true, true)
	got := buf.String()

	if !strings.Contains(got, " burst for ") {
		t.Errorf("expected announce burst-for annotation, got:\n%v", got)
	}
	// The PR burst annotation has no leading space and follows the Path
	// Rqs. incoming line.
	if !strings.Contains(got, "burst for ") {
		t.Errorf("expected pr burst-for annotation, got:\n%v", got)
	}

	// No bursts → no burst annotations.
	var buf2 bytes.Buffer
	ifstat.BurstActive = false
	ifstat.PrBurstActive = false
	renderInterface(&buf2, ifstat, true, true)
	got2 := buf2.String()
	if strings.Contains(got2, "burst for ") {
		t.Errorf("expected no burst annotation when idle, got:\n%v", got2)
	}
}

// TestRenderInterfaceInternalModeIndicator verifies the " (a>i)" mode
// annotation (Python rnstatus.py:432) and the MODE_INTERNAL label.
func TestRenderInterfaceInternalModeIndicator(t *testing.T) {
	t.Parallel()
	a2i := true
	ifstat := rns.InterfaceStat{
		Name:                "RNodeInterface[LoRa 915]",
		Status:              true,
		Mode:                modeAccessPoint,
		AnnouncesToInternal: &a2i,
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, false, false)
	got := buf.String()
	if !strings.Contains(got, "    Mode      : Access Point (a>i)\n") {
		t.Errorf("expected (a>i) annotation, got:\n%v", got)
	}

	// MODE_INTERNAL label.
	var buf2 bytes.Buffer
	ifstat.Mode = modeInternal
	ifstat.AnnouncesToInternal = nil
	renderInterface(&buf2, ifstat, false, false)
	if !strings.Contains(buf2.String(), "    Mode      : Internal\n") {
		t.Errorf("expected Internal mode, got:\n%v", buf2.String())
	}

	// Internal + (a>i).
	var buf3 bytes.Buffer
	ifstat.AnnouncesToInternal = &a2i
	renderInterface(&buf3, ifstat, false, false)
	if !strings.Contains(buf3.String(), "    Mode      : Internal (a>i)\n") {
		t.Errorf("expected Internal (a>i), got:\n%v", buf3.String())
	}
}

// TestRenderInterfaceAnnounceRateTarget verifies the
// announce-rate target/penalty/grace annotation
// (Python rnstatus.py:557-565).
func TestRenderInterfaceAnnounceRateTarget(t *testing.T) {
	t.Parallel()
	ifstat := rns.InterfaceStat{
		Name:                "RNodeInterface[LoRa 915]",
		Status:              true,
		Mode:                modeFull,
		InAnnounceFreq:      new(0.5),
		OutAnnounceFreq:     new(1.2),
		AnnounceRateTarget:  new(30),
		AnnounceRatePenalty: new(10),
		AnnounceRateGrace:   new(5),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, true, false)
	got := buf.String()
	if !strings.Contains(got, "(t:30s/p:10s/g:5)") {
		t.Errorf("expected announce-rate annotation (t:30s/p:10s/g:5), got:\n%v", got)
	}

	// Only target present (penalty absent → no /p, /g).
	var buf2 bytes.Buffer
	ifstat.AnnounceRatePenalty = nil
	ifstat.AnnounceRateGrace = nil
	renderInterface(&buf2, ifstat, true, false)
	if !strings.Contains(buf2.String(), "(t:30s)") {
		t.Errorf("expected (t:30s) annotation, got:\n%v", buf2.String())
	}
	if strings.Contains(buf2.String(), "/p:") {
		t.Errorf("did not expect /p annotation without penalty, got:\n%v", buf2.String())
	}
}

// TestRenderInterfacePerClientRate verifies the per-client ("/c") and
// per-peer ("/p") announce rate annotation
// (Python rnstatus.py:583-590, AutoInterface per-peer rate).
func TestRenderInterfacePerClientRate(t *testing.T) {
	t.Parallel()
	clients := 2
	ifstat := rns.InterfaceStat{
		Name:            "RNodeInterface[LoRa 915]",
		Status:          true,
		Mode:            modeFull,
		Clients:         &clients,
		InAnnounceFreq:  new(1.0),
		OutAnnounceFreq: new(4.0),
	}
	var buf bytes.Buffer
	renderInterface(&buf, ifstat, true, false)
	got := buf.String()
	if !strings.Contains(got, "2.0 Hz/c") {
		t.Errorf("expected per-client rate 2.0 Hz/c, got:\n%v", got)
	}

	// AutoInterface: no clients, peers present → per-peer rate with "/p".
	peers := 4
	auto := rns.InterfaceStat{
		Name:            "AutoInterface[test]",
		Status:          true,
		Mode:            modeAccessPoint,
		Peers:           &peers,
		InAnnounceFreq:  new(1.0),
		OutAnnounceFreq: new(4.0),
	}
	var buf2 bytes.Buffer
	renderInterface(&buf2, auto, true, false)
	got2 := buf2.String()
	if !strings.Contains(got2, "1.0 Hz/p") {
		t.Errorf("expected per-peer rate 1.0 Hz/p, got:\n%v", got2)
	}
}
