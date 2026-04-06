// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

const tempDirPrefix = "gornstatus-test-"

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

func TestSortInterfaces(t *testing.T) {
	t.Parallel()
	mkIfaces := func() []rns.InterfaceStat {
		return []rns.InterfaceStat{
			{Name: "A", Bitrate: 100, RXB: 300, TXB: 200, RXS: 10, TXS: 20},
			{Name: "B", Bitrate: 300, RXB: 100, TXB: 400, RXS: 30, TXS: 10},
			{Name: "C", Bitrate: 200, RXB: 200, TXB: 100, RXS: 20, TXS: 30},
		}
	}

	tests := []struct {
		name        string
		sortKey     string
		sortReverse bool
		wantOrder   string
	}{
		{"rate descending", "rate", false, "BCA"},
		{"rate ascending", "rate", true, "ACB"},
		{"bitrate alias", "bitrate", false, "BCA"},
		{"rx descending", "rx", false, "ACB"},
		{"tx descending", "tx", false, "BAC"},
		{"traffic descending", "traffic", false, "ABC"},
		{"rxs descending", "rxs", false, "BCA"},
		{"txs descending", "txs", false, "CAB"},
		{"unknown key no change", "unknown", false, "ABC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ifaces := mkIfaces()
			sortInterfaces(ifaces, tc.sortKey, tc.sortReverse)
			var got string
			for _, iface := range ifaces {
				got += iface.Name
			}
			if got != tc.wantOrder {
				t.Errorf("sortInterfaces(%q, %v) order = %q, want %q",
					tc.sortKey, tc.sortReverse, got, tc.wantOrder)
			}
		})
	}
}

func TestSortInterfacesAnnounces(t *testing.T) {
	t.Parallel()
	ifaces := []rns.InterfaceStat{
		{Name: "A", InAnnounceFreq: float64Ptr(1.0), OutAnnounceFreq: float64Ptr(2.0), HeldAnnounces: intPtr(5)},
		{Name: "B", InAnnounceFreq: float64Ptr(5.0), OutAnnounceFreq: float64Ptr(5.0), HeldAnnounces: intPtr(1)},
		{Name: "C", InAnnounceFreq: float64Ptr(3.0), OutAnnounceFreq: float64Ptr(1.0), HeldAnnounces: intPtr(3)},
	}

	tests := []struct {
		name      string
		sortKey   string
		wantOrder string
	}{
		{"announces desc", "announces", "BCA"},
		{"arx desc", "arx", "BCA"},
		{"atx desc", "atx", "BAC"},
		{"held desc", "held", "ACB"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cp := make([]rns.InterfaceStat, len(ifaces))
			copy(cp, ifaces)
			sortInterfaces(cp, tc.sortKey, false)
			var got string
			for _, iface := range cp {
				got += iface.Name
			}
			if got != tc.wantOrder {
				t.Errorf("sortInterfaces(%q) order = %q, want %q",
					tc.sortKey, got, tc.wantOrder)
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
