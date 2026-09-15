// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// netFixture wires the announce cache and path table together the way the bot
// does, so the directory command is exercised end to end.
func netFixture(t *testing.T) *watchFixture {
	t.Helper()
	f := newWatchFixture(t)
	// The net command reads the registry's path table, which the watch fixture
	// keeps separate because the watch commands only need the cache.
	f.reg.paths = f.paths
	return f
}

// TestNetCommandListsHeardServices asserts the directory names every service
// the bot has heard, with its hop count, its interface, and its hash prefix,
// nearest first.
func TestNetCommandListsHeardServices(t *testing.T) {
	t.Parallel()

	f := netFixture(t)
	hub := mustHex("a012129c10205c0b9441fcd2b755b2a7")
	node := mustHex("c7d0e71c10205c0b9441fcd2b755b2a7")
	lxmf := mustHex("4643602e6f3b1c0d8a9b7e5f4d3c2b1a")
	f.announce(t, "rrc.hub", "General", hub, peerHashFor(0x31))
	f.announce(t, "nomadnetwork.node", "WeatherStation", node, peerHashFor(0x32))
	f.announce(t, "lxmf.propagation", "", lxmf, peerHashFor(0x33))
	f.paths.setPath(hub, &rns.PathInfo{Hops: 1, Hash: hub, Timestamp: f.clock})
	f.paths.setPath(node, &rns.PathInfo{Hops: 2, Hash: node, Timestamp: f.clock})
	f.paths.setPath(lxmf, &rns.PathInfo{Hops: 2, Hash: lxmf, Timestamp: f.clock})

	lines := f.line(t, "net")
	if len(lines) != 4 {
		t.Fatalf("net = %v, want a header and three services", lines)
	}
	if lines[0] != "Known mesh services (within 3 hops):" {
		t.Errorf("net header = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], `[1 hop] Hub "General" (hash a012129c1020) via `) {
		t.Errorf("first row = %q, want the one-hop hub", lines[1])
	}
	// The two-hop services sort by destination hash, so the LXMF node (4…)
	// precedes the NomadNet node (c…).
	if !strings.HasPrefix(lines[2], "[2 hops] LXMF Propagation Node (hash 4643602e6f3b) via ") {
		t.Errorf("second row = %q, want the LXMF propagation node", lines[2])
	}
	if !strings.HasPrefix(lines[3], "[2 hops] NomadNet Node \"WeatherStation\" (hash c7d0e71c1020) via ") {
		t.Errorf("third row = %q, want the NomadNet node", lines[3])
	}
}

// TestNetCommandHonoursTheHopLimit asserts a smaller limit hides the services
// that are further away.
func TestNetCommandHonoursTheHopLimit(t *testing.T) {
	t.Parallel()

	f := netFixture(t)
	hub := mustHex("a012129c10205c0b9441fcd2b755b2a7")
	node := mustHex("c7d0e71c10205c0b9441fcd2b755b2a7")
	f.announce(t, "rrc.hub", "General", hub, peerHashFor(0x31))
	f.announce(t, "nomadnetwork.node", "WeatherStation", node, peerHashFor(0x32))
	f.paths.setPath(hub, &rns.PathInfo{Hops: 1, Hash: hub, Timestamp: f.clock})
	f.paths.setPath(node, &rns.PathInfo{Hops: 4, Hash: node, Timestamp: f.clock})

	lines := f.line(t, "net 2")
	if len(lines) != 2 {
		t.Fatalf("net 2 = %v, want a header and the one-hop hub only", lines)
	}
	if lines[0] != "Known mesh services (within 2 hops):" {
		t.Errorf("net 2 header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "Hub") {
		t.Errorf("net 2 row = %q, want the hub", lines[1])
	}

	// A limit that reaches nothing says so rather than showing an empty header.
	none := f.line(t, "net 1")
	if len(none) != 2 || !strings.Contains(none[1], "Hub") {
		t.Fatalf("net 1 = %v, want the one-hop hub", none)
	}
	f.paths.table = map[string]*rns.PathInfo{
		string(hub):  {Hops: 4, Hash: hub},
		string(node): {Hops: 4, Hash: node},
	}
	empty := f.line(t, "net 1")
	if len(empty) != 1 || empty[0] != "no mesh services heard within 1 hop" {
		t.Errorf("net 1 with only far services = %v, want the out-of-range line", empty)
	}
}

// TestNetCommandReportsAnnouncedButUnreachableServices asserts a service the
// bot has heard but has no path to is listed as such, because that is exactly
// the state an operator needs to see.
func TestNetCommandReportsAnnouncedButUnreachableServices(t *testing.T) {
	t.Parallel()

	f := netFixture(t)
	hub := mustHex("a012129c10205c0b9441fcd2b755b2a7")
	node := mustHex("c7d0e71c10205c0b9441fcd2b755b2a7")
	f.announce(t, "rrc.hub", "General", hub, peerHashFor(0x31))
	f.announce(t, "nomadnetwork.node", "WeatherStation", node, peerHashFor(0x32))
	f.paths.setPath(hub, &rns.PathInfo{Hops: 1, Hash: hub, Timestamp: f.clock})

	lines := f.line(t, "net")
	if len(lines) != 3 {
		t.Fatalf("net = %v, want a header, a reached hub, and an unreached node", lines)
	}
	if !strings.HasPrefix(lines[1], "[1 hop] Hub") {
		t.Errorf("first row = %q, want the reached hub", lines[1])
	}
	if !strings.HasPrefix(lines[2], `[no path] NomadNet Node "WeatherStation" (hash c7d0e71c1020)`) {
		t.Errorf("second row = %q, want the unreached node reported as such", lines[2])
	}
}

// TestNetCommandReportsAnEmptyNetwork asserts a bot that has heard nothing says
// so, and one with no transport at all says that instead.
func TestNetCommandReportsAnEmptyNetwork(t *testing.T) {
	t.Parallel()

	f := netFixture(t)
	lines := f.line(t, "net")
	if len(lines) != 1 || lines[0] != netNoServicesLine {
		t.Fatalf("net with nothing heard = %v, want %q", lines, netNoServicesLine)
	}

	f.reg.paths = nil
	lines = f.line(t, "net")
	if len(lines) != 1 || lines[0] != netUnavailableLine {
		t.Errorf("net without a transport = %v, want %q", lines, netUnavailableLine)
	}
}

// TestNetCommandExplainsTheNearFilter asserts near is accepted but answers with
// the reason an RNS announce cannot be filtered by position.
func TestNetCommandExplainsTheNearFilter(t *testing.T) {
	t.Parallel()

	f := netFixture(t)
	lines := f.line(t, "net near 849VCWC8+R9")
	if len(lines) != 1 || lines[0] != netNearUnsupportedLine {
		t.Errorf("net near = %v, want the explanation", lines)
	}

	bad := f.line(t, "net near nowhere at all")
	if len(bad) != 1 || !strings.Contains(bad[0], "no location found") {
		t.Errorf("net near with an unplaceable location = %v, want the notation help", bad)
	}
}

// TestNetCommandRejectsBadArguments asserts a hop limit outside the useful
// range, and a stray word, are refused with the usage.
func TestNetCommandRejectsBadArguments(t *testing.T) {
	t.Parallel()

	f := netFixture(t)
	for _, line := range []string{"net 0", "net 99", "net -1", "net bogus"} {
		lines := f.line(t, line)
		if len(lines) == 0 || (!strings.Contains(lines[0], "Usage: "+netUsage) &&
			!strings.Contains(lines[0], "hop limit")) {
			t.Errorf("%q = %v, want the usage or the hop-limit line", line, lines)
		}
	}
	if lines := f.line(t, "net near"); len(lines) == 0 || !strings.Contains(lines[0], "Usage: "+netUsage) {
		t.Errorf("net near with nothing after it = %v, want the usage line", lines)
	}
}

// TestNetServiceLabelNamesEveryKnownAspect asserts each announced aspect is
// rendered as the service an operator thinks of, and that an unknown aspect is
// reported by its own name rather than swallowed.
func TestNetServiceLabelNamesEveryKnownAspect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		aspect string
		name   string
		want   string
	}{
		{"rrc.hub", "General", `Hub "General"`},
		{"rrc.hub", "", "Hub"},
		{"lxmf.delivery", "", "LXMF Delivery"},
		{"lxmf.propagation", "", "LXMF Propagation Node"},
		{"nomadnetwork.node", "WeatherStation", `NomadNet Node "WeatherStation"`},
		{"something.else", "Odd", `something.else "Odd"`},
		{"something.else", "", "something.else"},
	}
	for _, tc := range tests {
		got := netServiceLabel(announce{Aspect: tc.aspect, Name: tc.name})
		if got != tc.want {
			t.Errorf("netServiceLabel(%v, %q) = %q, want %q", tc.aspect, tc.name, got, tc.want)
		}
	}
}

// TestAnnounceSnapshotAgesOutExpiredEntries asserts the directory never shows
// an announce the cache itself would no longer answer for.
func TestAnnounceSnapshotAgesOutExpiredEntries(t *testing.T) {
	t.Parallel()

	f := netFixture(t)
	f.announce(t, "rrc.hub", "General", mustHex("a012129c10205c0b9441fcd2b755b2a7"), peerHashFor(0x31))

	if got := len(f.cache.snapshot(f.clock)); got != 1 {
		t.Fatalf("fresh snapshot holds %v entries, want 1", got)
	}
	if got := len(f.cache.snapshot(f.clock.Add(announceTTL + time.Minute))); got != 0 {
		t.Errorf("expired snapshot holds %v entries, want none", got)
	}
}
