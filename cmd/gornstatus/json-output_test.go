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
		RXB: 1000,
		TXB: 2000,
		RXS: 100,
		TXS: 200,
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
