// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func intPtr(v int) *int { return &v }

func TestModeString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode int
		want string
	}{
		{modeFull, "Full"},
		{modeAccessPoint, "Access Point"},
		{modePointToPoint, "Point-to-Point"},
		{modeRoaming, "Roaming"},
		{modeBoundary, "Boundary"},
		{modeGateway, "Gateway"},
		{0, "Full"},
		{99, "Full"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := modeString(tc.mode)
			if got != tc.want {
				t.Errorf("modeString(%v) = %q, want %q", tc.mode, got, tc.want)
			}
		})
	}
}

func TestClientsString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		ifName  string
		clients *int
		want    string
	}{
		{"nil clients", "RNodeInterface[LoRa]", nil, ""},
		{"normal 5 clients", "RNodeInterface[LoRa]", intPtr(5), "Clients   : 5"},
		{"normal 0 clients", "RNodeInterface[LoRa]", intPtr(0), "Clients   : 0"},
		{"shared 1 program", "Shared Instance[37428]", intPtr(2), "Serving   : 1 program"},
		{"shared 0 programs", "Shared Instance[37428]", intPtr(1), "Serving   : 0 programs"},
		{"shared 3 programs", "Shared Instance[37428]", intPtr(4), "Serving   : 3 programs"},
		{"i2p 1 endpoint", "I2PInterface[test]", intPtr(1), "Peers     : 1 connected I2P endpoint"},
		{"i2p 3 endpoints", "I2PInterface[test]", intPtr(3), "Peers     : 3 connected I2P endpoints"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := clientsString(tc.ifName, tc.clients)
			if got != tc.want {
				t.Errorf("clientsString(%q, %v) = %q, want %q", tc.ifName, tc.clients, got, tc.want)
			}
		})
	}
}

func TestSpeedStr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0.00 bps"},
		{1, "1.00 bps"},
		{999, "999.00 bps"},
		{1000, "1.00 kbps"},
		{1500, "1.50 kbps"},
		{999999, "1000.00 kbps"},
		{1000000, "1.00 Mbps"},
		{1500000000, "1.50 Gbps"},
		{999999999999999, "1000.00 Tbps"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := speedStr(tc.input)
			if got != tc.want {
				t.Errorf("speedStr(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSizeStr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{999, "999 B"},
		{1000, "1.00 KB"},
		{1500, "1.50 KB"},
		{999999, "1000.00 KB"},
		{1000000, "1.00 MB"},
		{1500000000, "1.50 GB"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := rns.PrettySize(tc.input, "B")
			if got != tc.want {
				t.Errorf("PrettySize(%v, \"B\") = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
