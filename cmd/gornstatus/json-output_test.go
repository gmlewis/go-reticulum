// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestRenderJSON(t *testing.T) {
	t.Parallel()
	stats := &rns.InterfaceStatsSnapshot{
		RXB:         1000,
		TXB:         2000,
		RXS:         100,
		TXS:         200,
		TransportID: []byte{0xab, 0xcd},
		Interfaces: []rns.InterfaceStat{
			{
				Name:          "RNodeInterface[LoRa]",
				Type:          "RNodeInterface",
				Status:        true,
				Mode:          modeFull,
				Bitrate:       1200,
				RXB:           500,
				TXB:           600,
				IFACSignature: []byte{0x01, 0x02, 0x03},
			},
		},
	}
	var buf bytes.Buffer
	if err := renderJSON(&buf, stats); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v\nJSON: %v", err, buf.String())
	}

	for _, key := range []string{
		"interfaces", "rxb", "txb", "rxs", "txs", "transport_id",
	} {
		if _, ok := result[key]; !ok {
			t.Errorf("JSON missing top-level key %q", key)
		}
	}

	transportID, ok := result["transport_id"].(string)
	if !ok || transportID != "abcd" {
		t.Errorf("transport_id = %v, want %q", result["transport_id"], "abcd")
	}

	ifaces, ok := result["interfaces"].([]any)
	if !ok || len(ifaces) != 1 {
		t.Fatalf("interfaces = %v, want 1 element", result["interfaces"])
	}

	iface, ok := ifaces[0].(map[string]any)
	if !ok {
		t.Fatalf("interface[0] not a map")
	}

	for _, key := range []string{
		"name", "type", "status", "mode", "bitrate", "rxb", "txb",
		"ifac_signature",
	} {
		if _, ok := iface[key]; !ok {
			t.Errorf("interface missing key %q", key)
		}
	}

	sig, ok := iface["ifac_signature"].(string)
	if !ok || sig != "010203" {
		t.Errorf("ifac_signature = %v, want %q", iface["ifac_signature"], "010203")
	}
}

func TestBytesToHex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input []byte
		want  string
	}{
		{"nil", nil, ""},
		{"empty", []byte{}, ""},
		{"bytes", []byte{0xab, 0xcd, 0xef}, "abcdef"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := bytesToHex(tc.input)
			if got != tc.want {
				t.Errorf("bytesToHex(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestRenderJSONPrBurstAnnounceFields covers Phase 22 task 2: the JSON output
// (`rnstatus -j`) includes the path-request, burst, announce-rate, and
// announces-to-internal fields that Python's ifstats dict always emits
// (RNS/Reticulum.py:1478-1489). The Go InterfaceStat already carries these
// (populated from the local ifstats dict, rpc.go:548-558) but the JSON struct
// omitted them, so `gornstatus -j` was missing incoming_pr_frequency,
// outgoing_pr_frequency, announce_rate_target/penalty/grace, burst_active,
// burst_activated, pr_burst_active, pr_burst_activated, and
// announces_to_internal. Golden field set captured from RNS 1.4.2.
func TestRenderJSONPrBurstAnnounceFields(t *testing.T) {
	t.Parallel()
	inPr := 0.5
	outPr := 0.25
	artTarget := 3600
	artPenalty := 0
	artGrace := 5
	ati := true
	stats := &rns.InterfaceStatsSnapshot{
		Interfaces: []rns.InterfaceStat{
			{
				Name:                "RNodeInterface[LoRa]",
				Type:                "RNodeInterface",
				Status:              true,
				Mode:                modeFull,
				InPrFreq:            &inPr,
				OutPrFreq:           &outPr,
				AnnounceRateTarget:  &artTarget,
				AnnounceRatePenalty: &artPenalty,
				AnnounceRateGrace:   &artGrace,
				BurstActive:         true,
				BurstActivated:      1700000000.0,
				PrBurstActive:       false,
				PrBurstActivated:    0.0,
				AnnouncesToInternal: &ati,
			},
		},
	}
	var buf bytes.Buffer
	if err := renderJSON(&buf, stats); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal: %v\nJSON: %v", err, buf.String())
	}
	ifaces, ok := result["interfaces"].([]any)
	if !ok || len(ifaces) != 1 {
		t.Fatalf("interfaces = %v, want 1 element", result["interfaces"])
	}
	iface, ok := ifaces[0].(map[string]any)
	if !ok {
		t.Fatalf("interface[0] not a map")
	}

	// All the Python ifstats keys that were previously missing from the JSON
	// output must now be present.
	for _, key := range []string{
		"incoming_pr_frequency", "outgoing_pr_frequency",
		"announce_rate_target", "announce_rate_penalty", "announce_rate_grace",
		"burst_active", "burst_activated",
		"pr_burst_active", "pr_burst_activated",
		"announces_to_internal",
	} {
		if _, ok := iface[key]; !ok {
			t.Errorf("interface JSON missing key %q (Phase 22 task 2 parity field)", key)
		}
	}

	// Spot-check the values round-trip faithfully.
	if got, want := iface["incoming_pr_frequency"], 0.5; got != want {
		t.Errorf("incoming_pr_frequency = %v, want %v", got, want)
	}
	if got, want := iface["outgoing_pr_frequency"], 0.25; got != want {
		t.Errorf("outgoing_pr_frequency = %v, want %v", got, want)
	}
	if got, want := iface["burst_active"], true; got != want {
		t.Errorf("burst_active = %v, want %v", got, want)
	}
	if got, want := iface["pr_burst_active"], false; got != want {
		t.Errorf("pr_burst_active = %v, want %v", got, want)
	}
	if got, ok := iface["announce_rate_target"].(float64); !ok || int(got) != 3600 {
		t.Errorf("announce_rate_target = %v, want 3600", iface["announce_rate_target"])
	}
	if got, ok := iface["announces_to_internal"].(bool); !ok || !got {
		t.Errorf("announces_to_internal = %v, want true", iface["announces_to_internal"])
	}
}
