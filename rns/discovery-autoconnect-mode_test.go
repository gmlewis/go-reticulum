// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// TestInterfaceDiscoveryAutoconnectTransportEnabledSetsGatewayMode covers
// Phase 11 task 11: when transport is enabled, an autoconnected discovered
// interface is set to MODE_GATEWAY (AC_TRANSPORT_MODE) and gets the instance
// announce-rate defaults + AC_GRAVITY gravity (RNS/Discovery.py:742-752).
func TestInterfaceDiscoveryAutoconnectTransportEnabledSetsGatewayMode(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	ts.SetEnabled(true)
	discovery := NewInterfaceDiscovery(&Reticulum{
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	})
	discovery.backboneFactory = func(config discoveryBackboneClientConfig, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		return newBootstrapConstructorTestInterface(config.Name, "BackboneClientInterface"), nil
	}

	info, ok := mapToDiscoveredInterface(map[string]any{
		"name":         "Gateway Backbone",
		"type":         "BackboneInterface",
		"config_entry": "[[gw-backbone]]",
		"reachable_on": "127.0.0.1",
		"port":         4242,
		"network_id":   "01020304",
	})
	if !ok {
		t.Fatal("mapToDiscoveredInterface() = false, want true")
	}

	if err := discovery.autoconnect(info); err != nil {
		t.Fatalf("autoconnect() error = %v", err)
	}
	ifaces := ts.GetInterfaces()
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", len(ifaces))
	}
	iface := ifaces[0]

	if got := iface.Mode(); got != interfaces.ModeGateway {
		t.Fatalf("Mode() = %v, want %v (MODE_GATEWAY, AC_TRANSPORT_MODE)", got, interfaces.ModeGateway)
	}
	if got := iface.Gravity(); got != 0 {
		t.Fatalf("Gravity() = %v, want 0 (AC_GRAVITY)", got)
	}
	if v := iface.AnnounceRateTarget(); v == nil || *v != interfaces.DefaultARTarget {
		t.Fatalf("AnnounceRateTarget() = %v, want %v (default ar target)", v, interfaces.DefaultARTarget)
	}
	if v := iface.AnnounceRateGrace(); v == nil || *v != interfaces.DefaultARGrace {
		t.Fatalf("AnnounceRateGrace() = %v, want %v (default ar grace)", v, interfaces.DefaultARGrace)
	}
	if v := iface.AnnounceRatePenalty(); v == nil || *v != interfaces.DefaultARPenalty {
		t.Fatalf("AnnounceRatePenalty() = %v, want %v (default ar penalty)", v, interfaces.DefaultARPenalty)
	}
}

// TestInterfaceDiscoveryAutoconnectTransportDisabledLeavesModeUnset covers
// Phase 11 task 11: when transport is NOT enabled, the autoconnected
// interface mode is left unchanged (Python passes mode=None) and no
// announce-rate defaults are applied (RNS/Discovery.py:744,748-750).
func TestInterfaceDiscoveryAutoconnectTransportDisabledLeavesModeUnset(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	ts := NewTransportSystem(logger)
	// transport NOT enabled.
	discovery := NewInterfaceDiscovery(&Reticulum{
		transport:           ts,
		logger:              logger,
		autoconnectDiscover: 1,
	})
	discovery.backboneFactory = func(config discoveryBackboneClientConfig, handler interfaces.InboundHandler) (interfaces.Interface, error) {
		return newBootstrapConstructorTestInterface(config.Name, "BackboneClientInterface"), nil
	}

	info, ok := mapToDiscoveredInterface(map[string]any{
		"name":         "Plain Backbone",
		"type":         "BackboneInterface",
		"config_entry": "[[plain-backbone]]",
		"reachable_on": "127.0.0.1",
		"port":         4242,
		"network_id":   "01020304",
	})
	if !ok {
		t.Fatal("mapToDiscoveredInterface() = false, want true")
	}

	if err := discovery.autoconnect(info); err != nil {
		t.Fatalf("autoconnect() error = %v", err)
	}
	ifaces := ts.GetInterfaces()
	if len(ifaces) != 1 {
		t.Fatalf("expected 1 auto-connected interface, got %v", len(ifaces))
	}
	iface := ifaces[0]

	// newBootstrapConstructorTestInterface starts at ModeFull; with transport
	// disabled the autoconnect path must not rewrite it to MODE_GATEWAY.
	if got := iface.Mode(); got == interfaces.ModeGateway {
		t.Fatalf("Mode() = MODE_GATEWAY, want unchanged (transport disabled → mode=None)")
	}
	if iface.AnnounceRateTarget() != nil {
		t.Fatalf("AnnounceRateTarget() = %v, want nil (no ar defaults when transport disabled)", iface.AnnounceRateTarget())
	}
}
