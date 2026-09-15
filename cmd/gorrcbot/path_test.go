// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// fakePaths is a path table the tests control: which destinations have a path,
// what the entry looks like, which identities can be recalled, and every path
// request that was fired.
type fakePaths struct {
	table      map[string]*rns.PathInfo
	ids        map[string]*rns.Identity
	recallErr  map[string]error
	count      int
	requested  []string
	requestErr error
}

func newFakePaths() *fakePaths {
	return &fakePaths{
		table:     map[string]*rns.PathInfo{},
		ids:       map[string]*rns.Identity{},
		recallErr: map[string]error{},
	}
}

// setPath installs one path-table entry for a destination hash.
func (f *fakePaths) setPath(hash []byte, entry *rns.PathInfo) {
	f.table[string(hash)] = entry
	f.count = len(f.table)
}

// setIdentity makes one identity hash recallable.
func (f *fakePaths) setIdentity(id *rns.Identity) {
	f.ids[string(id.Hash)] = id
}

func (f *fakePaths) HasPath(hash []byte) bool { return f.table[string(hash)] != nil }

func (f *fakePaths) GetPathEntry(hash []byte) *rns.PathInfo { return f.table[string(hash)] }

func (f *fakePaths) RequestPath(hash []byte) error {
	f.requested = append(f.requested, hexString(hash))
	return f.requestErr
}

func (f *fakePaths) PathCount() int { return f.count }

func (f *fakePaths) Recall(hash []byte) *rns.Identity {
	if err := f.recallErr[string(hash)]; err != nil {
		return nil
	}
	return f.ids[string(hash)]
}

// pathFixture builds a registry with a controllable path table and one peer the
// hub knows in #general.
func pathFixture(t *testing.T, peerNick string, peerIdentity *rns.Identity) (*registry, *hubSession, *fakeHub, *fakePaths) {
	t.Helper()
	cfg := defaultTestConfig()
	reg, session, fake := commandFixture(t, cfg)
	paths := newFakePaths()
	reg.paths = paths
	fake.setKnownPeer(hexString(peerIdentity.Hash), peerNick)
	return reg, session, fake, paths
}

// runPathLine runs one command line at the fixed base clock.
func runPathLine(t *testing.T, reg *registry, session *hubSession, line string) []string {
	t.Helper()
	return reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot "+line, peerHashFor(0x11)),
		Room:    "general",
		Command: line,
		Nick:    "gorrcbot",
		Now:     catchupBase,
	})
}

// TestPathReportsAKnownPeersDestinations asserts the path command derives a
// peer's destinations from its RRC identity, reports the hops, next hop, and
// freshness of each, and never prints the "unknown hops" sentinel as a number.
func TestPathReportsAKnownPeersDestinations(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _, paths := pathFixture(t, "Carol", peer)

	delivery := rns.CalculateHash(peer, "lxmf", "delivery")
	node := rns.CalculateHash(peer, "nomadnetwork", "node")
	nextHop := mustHex("9f3c9f3c9f3c9f3c9f3c9f3c9f3c9f3c")
	paths.setIdentity(peer)
	paths.setPath(delivery, &rns.PathInfo{
		Timestamp: catchupBase.Add(-12 * time.Minute),
		Hash:      delivery,
		NextHop:   nextHop,
		Hops:      2,
		Expires:   catchupBase.Add(6*24*time.Hour + 23*time.Hour),
	})
	paths.setPath(node, &rns.PathInfo{
		Timestamp: catchupBase.Add(-3 * time.Hour),
		Hash:      node,
		NextHop:   nextHop,
		Hops:      rns.PathfinderM,
		Expires:   catchupBase.Add(90 * time.Minute),
	})

	assertLines(t, runPathLine(t, reg, session, "path Carol"), []string{
		fmt.Sprintf("path Carol (%v): 2 destinations", shortHash(hexString(peer.Hash))),
		fmt.Sprintf("lxmf.delivery (%v): 2 hops via %v on None, learned 12m ago, expires in 6d",
			shortHash(hexString(delivery)), shortHash(hexString(nextHop))),
		fmt.Sprintf("nomadnetwork.node (%v): an unknown number of hops via %v on None, learned 3h ago, expires in 1h",
			shortHash(hexString(node)), shortHash(hexString(nextHop))),
	})
}

// TestPathAsksForAMissingPathOnce asserts a destination with no path is reported
// honestly and a path request is fired exactly once, since the dispatcher may not
// wait for the answer.
func TestPathAsksForAMissingPathOnce(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _, paths := pathFixture(t, "Carol", peer)
	paths.setIdentity(peer)

	delivery := rns.CalculateHash(peer, "lxmf", "delivery")
	node := rns.CalculateHash(peer, "nomadnetwork", "node")

	assertLines(t, runPathLine(t, reg, session, "path Carol"), []string{
		fmt.Sprintf("path Carol (%v): 2 destinations", shortHash(hexString(peer.Hash))),
		fmt.Sprintf("lxmf.delivery (%v): %v", shortHash(hexString(delivery)), pathAskedLine),
		fmt.Sprintf("nomadnetwork.node (%v): %v", shortHash(hexString(node)), pathAskedLine),
	})
	if want := 2; len(paths.requested) != want {
		t.Fatalf("path requests = %v, want %v", paths.requested, want)
	}
	for _, hash := range []string{hexString(delivery), hexString(node)} {
		if !containsString(paths.requested, hash) {
			t.Errorf("no path request for %v; requested %v", hash, paths.requested)
		}
	}

	// A second ask sees the same table and asks again: the command never waits,
	// so every ask that finds nothing says so and fires one request.
	runPathLine(t, reg, session, "path Carol")
	if want := 4; len(paths.requested) != want {
		t.Errorf("path requests after two asks = %v, want %v", len(paths.requested), want)
	}
}

// TestPathRejectsUnknownAndAmbiguousTokens asserts an unusable token is answered
// with one honest line, never a guess and never a path request.
func TestPathRejectsUnknownAndAmbiguousTokens(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _, paths := pathFixture(t, "Carol", peer)

	assertLines(t, runPathLine(t, reg, session, "path nobody-here"), []string{"no such peer"})
	assertLines(t, runPathLine(t, reg, session, "path abc"), []string{
		"a hash prefix needs at least 6 characters; use the full hash or a nick",
	})
	if len(paths.requested) != 0 {
		t.Errorf("path requests = %v, want none for a rejected token", paths.requested)
	}
}

// TestPathReportsAPeerWithNoRecallableIdentity asserts a peer the transport has
// never seen an announce from is answered honestly: without the identity there is
// no destination to look up.
func TestPathReportsAPeerWithNoRecallableIdentity(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _, paths := pathFixture(t, "Carol", peer)
	paths.recallErr[string(peer.Hash)] = errors.New("no such identity")

	assertLines(t, runPathLine(t, reg, session, "path Carol"), []string{
		"no announce has been seen for Carol, so I cannot derive their destinations",
	})
	if len(paths.requested) != 0 {
		t.Errorf("path requests = %v, want none without an identity", paths.requested)
	}
}

// TestPathWithoutATransportIsHonest asserts the command says so rather than
// panicking when no transport is wired in.
func TestPathWithoutATransportIsHonest(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	reg, session, fake := commandFixture(t, cfg)
	fake.setKnownPeer(hexString(peerHashFor(0x33)), "Carol")

	assertLines(t, runPathLine(t, reg, session, "path Carol"), []string{pathUnavailableLine})
	assertLines(t, runPathLine(t, reg, session, "path"), []string{pathUnavailableLine})
}

// TestPathIncludesTheHubsOwnDestinationForTheHub asserts a hub's own identity gets
// the third destination an ordinary peer does not serve, and that an ordinary
// peer never does.
func TestPathIncludesTheHubsOwnDestinationForTheHub(t *testing.T) {
	t.Parallel()

	hub, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, fake, paths := pathFixture(t, "Hub", hub)
	paths.setIdentity(hub)
	fake.setHubIdentity(hub.Hash)

	hubDest := rns.CalculateHash(hub, "rrc", "hub")
	paths.setPath(hubDest, &rns.PathInfo{
		Timestamp: catchupBase.Add(-time.Minute),
		NextHop:   mustHex("9f3c9f3c9f3c9f3c9f3c9f3c9f3c9f3c"),
		Hops:      1,
		Expires:   catchupBase.Add(7 * 24 * time.Hour),
	})

	lines := runPathLine(t, reg, session, "path Hub")
	if len(lines) != 4 {
		t.Fatalf("lines = %v, want a header and three destinations for a hub", lines)
	}
	if got, want := lines[3], fmt.Sprintf("rrc.hub (%v): 1 hop via 9f3c9f3c9f3c on None, learned 1m ago, expires in 7d",
		shortHash(hexString(hubDest))); got != want {
		t.Errorf("hub line = %q, want %q", got, want)
	}
	if strings.Contains(strings.Join(lines, "\n"), "rrc.hub") == false {
		t.Error("a hub must be told about its own rrc.hub destination")
	}
}

// TestPathReportsAnExpiredPathHonestly asserts an entry the transport still holds
// but that has passed its expiry is reported as expired rather than as fresh.
func TestPathReportsAnExpiredPathHonestly(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _, paths := pathFixture(t, "Carol", peer)
	paths.setIdentity(peer)

	delivery := rns.CalculateHash(peer, "lxmf", "delivery")
	node := rns.CalculateHash(peer, "nomadnetwork", "node")
	paths.setPath(delivery, &rns.PathInfo{
		Timestamp: catchupBase.Add(-2 * time.Hour),
		NextHop:   mustHex("9f3c9f3c9f3c9f3c9f3c9f3c9f3c9f3c"),
		Hops:      3,
		Expires:   catchupBase.Add(-5 * time.Minute),
	})
	paths.setPath(node, &rns.PathInfo{
		Timestamp: catchupBase,
		NextHop:   mustHex("9f3c9f3c9f3c9f3c9f3c9f3c9f3c9f3c"),
		Hops:      3,
		Expires:   catchupBase.Add(30 * time.Second),
	})

	assertLines(t, runPathLine(t, reg, session, "path Carol"), []string{
		fmt.Sprintf("path Carol (%v): 2 destinations", shortHash(hexString(peer.Hash))),
		fmt.Sprintf("lxmf.delivery (%v): 3 hops via 9f3c9f3c9f3c on None, learned 2h ago, expired 5m ago",
			shortHash(hexString(delivery))),
		fmt.Sprintf("nomadnetwork.node (%v): 3 hops via 9f3c9f3c9f3c on None, learned 0s ago, expires in 30s",
			shortHash(hexString(node))),
	})
}

// TestPathWithNoArgumentsReportsTheTableSize asserts a bare path answers the
// cheap question "is this transport alive?" without needing a token.
func TestPathWithNoArgumentsReportsTheTableSize(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _, paths := pathFixture(t, "Carol", peer)
	paths.setPath(rns.CalculateHash(peer, "lxmf", "delivery"), &rns.PathInfo{Hops: 1})

	assertLines(t, runPathLine(t, reg, session, "path"), []string{
		"Usage: " + pathUsage,
		"the path table holds 1 destination",
	})
}

// TestPathResolvesANameTheHubNeverShowedUs asserts a token no room ever showed the
// hub but the announce cache heard is still answerable: announces are how a peer
// this client has never met becomes known at all.
func TestPathResolvesANameTheHubNeverShowedUs(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _, paths := pathFixture(t, "Carol", peer)
	paths.setIdentity(peer)
	delivery := rns.CalculateHash(peer, "lxmf", "delivery")
	paths.setPath(delivery, &rns.PathInfo{
		Timestamp: catchupBase.Add(-5 * time.Minute),
		NextHop:   mustHex("9f3c9f3c9f3c9f3c9f3c9f3c9f3c9f3c"),
		Hops:      1,
		Expires:   catchupBase.Add(time.Hour),
	})
	// The announce the transport heard, with the identity hash that makes the
	// peer's destinations derivable.
	reg.announces.process(announce{
		DestHex:     hexString(mustHex("abcdefabcdefabcdefabcdefabcdefab")),
		IdentityHex: hexString(peer.Hash),
		Name:        "Retibooks Node",
		Aspect:      "nomadnetwork.node",
		At:          catchupBase,
	})

	lines := runPathLine(t, reg, session, "path retibooks")
	if len(lines) != 3 {
		t.Fatalf("lines = %v, want a header and two destinations", lines)
	}
	if want := fmt.Sprintf("path Retibooks Node (%v): 2 destinations", shortHash(hexString(peer.Hash))); lines[0] != want {
		t.Errorf("header = %q, want %q", lines[0], want)
	}
	if !strings.Contains(lines[1], "1 hop via 9f3c9f3c9f3c") {
		t.Errorf("line = %q, want the cached path reported", lines[1])
	}
}

// TestPathRefusesANameWithNoIdentityBehindIt asserts a token that is neither a
// known peer nor an announce with an identity is refused with the hub's own
// answer, never guessed at.
func TestPathRefusesANameWithNoIdentityBehindIt(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _, _ := pathFixture(t, "Carol", peer)
	reg.announces.process(announce{
		DestHex: hexString(mustHex("abcdefabcdefabcdefabcdefabcdefab")),
		Name:    "Anonymous Node",
		Aspect:  "nomadnetwork.node",
		At:      catchupBase,
	})

	assertLines(t, runPathLine(t, reg, session, "path Anonymous"), []string{"no such peer"})
}

// TestPathDoesNotInventTheHubsDestinationBeforeTheWelcome asserts the hub's own
// rrc.hub destination is reported only for the hub's real identity: before a
// WELCOME the bot has no hub identity at all, and two empty hashes are not a
// match.
func TestPathDoesNotInventTheHubsDestinationBeforeTheWelcome(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, fake, paths := pathFixture(t, "Carol", peer)
	paths.setIdentity(peer)
	// A hub that never welcomed this client reports no identity hash.
	fake.setHubIdentity(nil)

	lines := runPathLine(t, reg, session, "path Carol")
	if got := len(lines); got != 3 {
		t.Fatalf("lines = %v, want a header and two destinations", lines)
	}
	for _, line := range lines {
		if strings.Contains(line, "rrc.hub") {
			t.Errorf("line %q claims a hub destination for an ordinary peer", line)
		}
	}
}

// TestPathIsRegistered asserts the command is in the table, so help lists it and
// the dispatcher can reach it.
func TestPathIsRegistered(t *testing.T) {
	t.Parallel()

	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, session, _, _ := pathFixture(t, "Carol", peer)
	cmd, ok := reg.byName["path"]
	if !ok {
		t.Fatalf("path is not registered; names = %v", reg.names())
	}
	if cmd.usage != pathUsage {
		t.Errorf("usage = %q, want %q", cmd.usage, pathUsage)
	}
	assertLines(t, reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot help path", peerHashFor(0x11)),
		Room:    "general",
		Command: "help path",
		Nick:    "gorrcbot",
		Now:     catchupBase,
	}), []string{
		"path — " + cmd.summary + ". Usage: " + pathUsage,
	})
}

// containsString reports whether want is in the slice.
func containsString(haystack []string, want string) bool {
	return slices.Contains(haystack, want)
}
