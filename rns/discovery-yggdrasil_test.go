// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// TestIsYggIPv6 verifies that isYggIPv6 mirrors Python's is_ygg_ipv6
// (RNS/Discovery.py:850-852) — an address is Yggdrasil when it parses as an
// IPv6 address inside the 200::/7 network. Non-IPv6 addresses, IPv4
// addresses, and unparseable strings return false.
func TestIsYggIPv6(t *testing.T) {
	t.Parallel()
	cases := []struct {
		addr string
		want bool
	}{
		// Inside 200::/7 — IPv6 network 0200:: to 03ff:: (first byte 0x02 or 0x03).
		{"200::1", true},
		{"200:1111:2222:3333:4444:5555:6666:7777", true},
		{"202::1", true},
		{"3ff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", true},
		// Outside 200::/7.
		{"400::1", false},
		{"::1", false},
		{"fe80::1", false},
		{"fd00::1", false},
		// IPv4 is not Yggdrasil.
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		// Unparseable.
		{"", false},
		{"discovery.example.net", false},
		{"not an address", false},
	}
	for _, c := range cases {
		if got := isYggIPv6(c.addr); got != c.want {
			t.Fatalf("isYggIPv6(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

// TestInterfaceDiscoveryAutoconnectYggdrasilReachableOnSkips verifies that
// when a discovered Backbone interface's reachable_on is a Yggdrasil IPv6
// address (200::/7), the autoconnect path returns early and no interface is
// created (RNS/Discovery.py:720-722).
func TestInterfaceDiscoveryAutoconnectYggdrasilReachableOnSkips(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	discovery := NewInterfaceDiscovery(&Reticulum{
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	})

	calls := 0
	discovery.backboneFactory = func(config discoveryBackboneClientConfig, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		calls++
		return newBootstrapConstructorTestInterface(config.Name, "BackboneClientInterface"), nil
	}

	info, ok := mapToDiscoveredInterface(map[string]any{
		"name":         "Yggdrasil Backbone",
		"type":         "BackboneInterface",
		"config_entry": "[[ygg-backbone]]",
		"reachable_on": "200:1111:2222:3333:4444:5555:6666:7777",
		"port":         4242,
		"network_id":   "01020304",
	})
	if !ok {
		t.Fatal("mapToDiscoveredInterface() = false, want true")
	}

	if err := discovery.autoconnect(info); err != nil {
		t.Fatalf("autoconnect() error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("backboneFactory calls = %v, want 0 (Yggdrasil reachable_on must not auto-connect)", calls)
	}
	if got := len(ts.GetInterfaces()); got != 0 {
		t.Fatalf("expected 0 auto-connected interfaces, got %v", got)
	}
}
