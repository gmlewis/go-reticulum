// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"net"
	"testing"
	"time"
)

// The AutoInterface discovery token hashes sha256(groupID || link_local), so
// when the kernel replaces an interface's IPv6 link-local address (Wi-Fi roam,
// wlan restart, DHCPv6 refresh) the node must re-adopt the new address,
// rebuild its bound sockets, and stop announcing the stale hash. Python
// resyncs every job tick (AutoInterface.py:405-455); the Go port previously
// adopted once at construction and never looked again, leaving the node
// permanently invisible on the LAN while TCP planes kept working.
//
// These tests pin the resync planning + application behavior that keeps
// adoptedInterfaces, the self-address set, echo bookkeeping, and the bound
// socket sets consistent across address churn.

func TestPlanAddressResyncClassifiesChanges(t *testing.T) {
	t.Parallel()
	oldA := net.ParseIP("fe80::aaaa")
	newA := net.ParseIP("fe80::bbbb")
	adopted := map[string]net.IP{"en0": oldA}
	next := map[string]net.IP{"en0": newA}

	got := planAddressResync(adopted, next)
	if len(got.changed) != 1 || got.changed["en0"].cur.String() != "fe80::bbbb" {
		t.Fatalf("expected en0 changed to fe80::bbbb, got %+v", got)
	}
	if len(got.removed) != 0 || len(got.added) != 0 {
		t.Fatalf("unexpected removed/added classifications: %+v", got)
	}

	// Vanished interface (NIC gone).
	next2 := map[string]net.IP{}
	got2 := planAddressResync(adopted, next2)
	if len(got2.removed) != 1 || got2.removed[0] != "en0" {
		t.Fatalf("expected en0 classified removed, got %+v", got2)
	}
	if len(got2.changed) != 0 {
		t.Fatalf("changed must be empty, got %+v", got2.changed)
	}

	// Unchanged address produces no actions.
	same := map[string]net.IP{"en0": oldA}
	got3 := planAddressResync(adopted, same)
	if len(got3.changed)+len(got3.removed)+len(got3.added) != 0 {
		t.Fatalf("no-op resync must classify nothing, got %+v", got3)
	}
}

func TestApplyAddressResyncUpdatesStateAndSockets(t *testing.T) {
	t.Parallel()
	oldIP := net.ParseIP("fe80::1")
	newIP := net.ParseIP("fe80::2")

	rebuilt := make(chan string, 4)
	closedIfaces := make(chan string, 4)

	ai := &AutoInterface{
		BaseInterface:     NewBaseInterface("auto-resync", ModeFull, AutoBitrateGuess),
		groupID:           []byte("reticulum"),
		adoptedInterfaces: map[string]net.IP{"en0": oldIP},
		linkLocalSet:      map[string]struct{}{"fe80::1": {}},
		sockets:           map[string]*autoSocketSet{"en0": nil},
		spawnedInterfaces: map[string]*AutoInterfacePeer{},
		multicastEchoes:   map[string]time.Time{},
		initialEchoes:     map[string]time.Time{},
		timedOutIfaces:    map[string]bool{},
	}
	ai.rebuildSockets = func(ifname string, ip net.IP) error {
		rebuilt <- ifname + "@" + ip.String()
		return nil
	}
	closeAutoSocketSet = func(s *autoSocketSet, ifname string) {
		closedIfaces <- ifname
	}

	plan := addrResyncPlan{
		changed: map[string]ipPair{"en0": {old: oldIP, cur: newIP}},
	}
	ai.applyAddressResync(plan)

	select {
	case got := <-rebuilt:
		if got != "en0@fe80::2" {
			t.Fatalf("rebuild called with %q, want en0@fe80::2", got)
		}
	default:
		t.Fatal("rebuild not invoked for changed address")
	}
	select {
	case got := <-closedIfaces:
		if got != "en0" {
			t.Fatalf("closed wrong iface %q", got)
		}
	default:
		t.Fatal("stale socket set was not closed")
	}

	ai.mu.Lock()
	defer ai.mu.Unlock()
	if got := ai.adoptedInterfaces["en0"].String(); got != "fe80::2" {
		t.Fatalf("adoptedInterfaces not updated: %q", got)
	}
	if _, ok := ai.linkLocalSet["fe80::1"]; ok {
		t.Fatal("stale link-local still present in self-address set")
	}
	if _, ok := ai.linkLocalSet["fe80::2"]; !ok {
		t.Fatal("fresh link-local missing from self-address set")
	}
	if _, ok := ai.sockets["en0"]; !ok {
		t.Fatal("sockets entry lost during rebuild")
	}
}

// Removal: when an interface vanishes entirely, state and peers bound to it
// must be dropped so peerAnnounce stops targeting it.
func TestApplyAddressResyncRemovesVanishedInterface(t *testing.T) {
	t.Parallel()
	ai := &AutoInterface{
		BaseInterface:     NewBaseInterface("auto-resync-rm", ModeFull, AutoBitrateGuess),
		adoptedInterfaces: map[string]net.IP{"wlan0": net.ParseIP("fe80::c1")},
		linkLocalSet:      map[string]struct{}{"fe80::c1": {}},
		sockets:           map[string]*autoSocketSet{"wlan0": nil},
		spawnedInterfaces: map[string]*AutoInterfacePeer{
			"fe80::d9": {BaseInterface: NewBaseInterface("p-d9", ModeFull, AutoBitrateGuess), interfaceName: "wlan0"},
		},
		multicastEchoes: map[string]time.Time{},
		initialEchoes:   map[string]time.Time{},
		timedOutIfaces:  map[string]bool{},
	}
	var closed []string
	closeAutoSocketSet = func(s *autoSocketSet, ifname string) { closed = append(closed, ifname) }

	ai.applyAddressResync(addrResyncPlan{removed: []string{"wlan0"}})

	if len(closed) != 1 || closed[0] != "wlan0" {
		t.Fatalf("vanished iface socket set not closed: %v", closed)
	}
	ai.mu.Lock()
	defer ai.mu.Unlock()
	if _, ok := ai.adoptedInterfaces["wlan0"]; ok {
		t.Fatal("vanished iface still adopted")
	}
	if _, ok := ai.spawnedInterfaces["fe80::d9"]; ok {
		t.Fatal("peer of vanished iface not culled")
	}
}
