// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeDiscoveryScope(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", AutoScopeLink},
		{"link", AutoScopeLink},
		{"admin", AutoScopeAdmin},
		{"site", AutoScopeSite},
		{"organisation", AutoScopeOrganisation},
		{"organization", AutoScopeOrganisation},
		{"global", AutoScopeGlobal},
		{"unknown", AutoScopeLink},
	}

	for _, tt := range tests {
		if got := normalizeDiscoveryScope(tt.in); got != tt.want {
			t.Fatalf("normalizeDiscoveryScope(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeMulticastAddressType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", AutoMulticastTemporary},
		{"temporary", AutoMulticastTemporary},
		{"permanent", AutoMulticastPermanent},
		{"invalid", AutoMulticastTemporary},
	}

	for _, tt := range tests {
		if got := normalizeMulticastAddressType(tt.in); got != tt.want {
			t.Fatalf("normalizeMulticastAddressType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAutoMulticastDiscoveryIPDeterministic(t *testing.T) {
	ip1 := autoMulticastDiscoveryIP([]byte("reticulum"), AutoMulticastTemporary, AutoScopeLink)
	ip2 := autoMulticastDiscoveryIP([]byte("reticulum"), AutoMulticastTemporary, AutoScopeLink)
	if ip1 == nil || ip2 == nil {
		t.Fatalf("autoMulticastDiscoveryIP returned nil")
	}
	if ip1.String() != ip2.String() {
		t.Fatalf("autoMulticastDiscoveryIP not deterministic: %q vs %q", ip1.String(), ip2.String())
	}
	if ip1.To16() == nil || ip1.To4() != nil {
		t.Fatalf("autoMulticastDiscoveryIP expected IPv6, got %q", ip1.String())
	}
}

func TestAutoProcessIncomingSuppressesDuplicateAcrossPeersWithinTTL(t *testing.T) {
	got := make(chan string, 4)
	ai := &AutoInterface{
		BaseInterface: NewBaseInterface("auto-test", ModeFull, AutoBitrateGuess),
		inboundHandler: func(data []byte, iface Interface) {
			got <- string(data)
		},
		spawnedInterfaces: map[string]*AutoInterfacePeer{},
		recentFrames:      map[[32]byte]time.Time{},
		multiIfDequeTTL:   30 * time.Millisecond,
	}
	atomic.StoreInt32(&ai.running, 1)
	atomic.StoreInt32(&ai.online, 1)

	ai.spawnedInterfaces["fe80::1"] = &AutoInterfacePeer{BaseInterface: NewBaseInterface("p1", ModeFull, AutoBitrateGuess), owner: ai, addr: "fe80::1", interfaceName: "if0"}
	ai.spawnedInterfaces["fe80::2"] = &AutoInterfacePeer{BaseInterface: NewBaseInterface("p2", ModeFull, AutoBitrateGuess), owner: ai, addr: "fe80::2", interfaceName: "if1"}

	payload := []byte("same-payload")
	ai.processIncoming(payload, "fe80::1")
	ai.processIncoming(payload, "fe80::2")

	select {
	case <-got:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("expected first payload delivery")
	}

	select {
	case d := <-got:
		t.Fatalf("expected duplicate suppression, got extra delivery %q", d)
	case <-time.After(80 * time.Millisecond):
	}

	time.Sleep(35 * time.Millisecond)
	ai.processIncoming(payload, "fe80::2")

	select {
	case <-got:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("expected payload delivery after dedupe TTL elapsed")
	}
}

func TestAutoUpdateInterfaceHealthTracksCarrierLossAndRecovery(t *testing.T) {
	now := time.Now()
	ai := &AutoInterface{
		BaseInterface:        NewBaseInterface("auto-health", ModeFull, AutoBitrateGuess),
		multicastEchoTimeout: 100 * time.Millisecond,
		adoptedInterfaces:    map[string]net.IP{"en0": net.ParseIP("fe80::1")},
		multicastEchoes:      map[string]time.Time{"en0": now.Add(-2 * time.Second)},
		initialEchoes:        map[string]time.Time{"en0": now.Add(-3 * time.Second)},
		timedOutIfaces:       map[string]bool{},
	}
	atomic.StoreInt32(&ai.running, 1)

	ai.mu.Lock()
	ai.updateInterfaceHealthLocked(now)
	ai.mu.Unlock()

	if ai.Status() {
		t.Fatalf("expected AutoInterface to be offline after carrier timeout")
	}
	if !ai.timedOutIfaces["en0"] {
		t.Fatalf("expected en0 timeout flag after carrier timeout")
	}

	ai.mu.Lock()
	ai.multicastEchoes["en0"] = now
	ai.updateInterfaceHealthLocked(now)
	ai.mu.Unlock()

	if !ai.Status() {
		t.Fatalf("expected AutoInterface to recover online after fresh echo")
	}
	if ai.timedOutIfaces["en0"] {
		t.Fatalf("expected en0 timeout flag cleared after recovery")
	}
}

func TestAutoUpdateInterfaceHealthDoesNotTimeoutBeforeInitialEcho(t *testing.T) {
	now := time.Now()
	ai := &AutoInterface{
		BaseInterface:        NewBaseInterface("auto-health-grace", ModeFull, AutoBitrateGuess),
		multicastEchoTimeout: 100 * time.Millisecond,
		adoptedInterfaces:    map[string]net.IP{"en0": net.ParseIP("fe80::1")},
		multicastEchoes:      map[string]time.Time{"en0": now.Add(-2 * time.Second)},
		initialEchoes:        map[string]time.Time{},
		timedOutIfaces:       map[string]bool{},
	}
	atomic.StoreInt32(&ai.running, 1)

	ai.mu.Lock()
	ai.updateInterfaceHealthLocked(now)
	ai.mu.Unlock()

	if !ai.Status() {
		t.Fatalf("expected AutoInterface to stay online before initial echo is observed")
	}
	if ai.timedOutIfaces["en0"] {
		t.Fatalf("expected en0 timeout flag to remain false before initial echo")
	}
}

func TestAutoReplaceAdoptedInterfaceAddressUpdatesLinkLocalSet(t *testing.T) {
	ai := &AutoInterface{
		adoptedInterfaces: map[string]net.IP{"en0": net.ParseIP("fe80::1")},
		linkLocalSet:      map[string]struct{}{"fe80::1": {}},
	}

	ai.replaceAdoptedInterfaceAddressLocked("en0", net.ParseIP("fe80::2"))

	if _, ok := ai.linkLocalSet["fe80::1"]; ok {
		t.Fatalf("expected old link-local address removed from set")
	}
	if _, ok := ai.linkLocalSet["fe80::2"]; !ok {
		t.Fatalf("expected new link-local address added to set")
	}
	if got := ai.adoptedInterfaces["en0"]; got == nil || got.String() != "fe80::2" {
		t.Fatalf("expected adopted interface address updated to fe80::2, got %v", got)
	}
}
