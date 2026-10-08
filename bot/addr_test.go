// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// addrDestHex derives a destination address the way the command must, so the
// expectations below are computed rather than pasted.
func addrDestHex(identityHash []byte, app string, aspects ...string) string {
	return hexString(rns.CalculateHash(&rns.Identity{Hash: identityHash}, app, aspects...))
}

// TestAddrCommandListsEveryAddress asserts a peer's reply: the identity in the
// form a hub addresses them by, then one line per destination, each in the form
// the client linkifies.
func TestAddrCommandListsEveryAddress(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	peer := peerHashFor(0x31)
	fake.setKnownPeer(hexString(peer), qrTestPeerNick)

	lines := runLines(t, reg, session, "addr "+qrTestPeerNick)

	want := []string{
		"addr " + qrTestPeerNick + ": identity @" + hexString(peer) + " (" + addrIdentityNote + ")",
		"lxmf@" + addrDestHex(peer, "lxmf", "delivery") + " (lxmf.delivery, where LXMF messages go)",
		addrDestHex(peer, "nomadnetwork", "node") + " (nomadnetwork.node, the node to browse)",
	}
	if len(lines) != len(want) {
		t.Fatalf("addr replied with %v lines, want %v: %q", len(lines), len(want), lines)
	}
	for i, line := range lines {
		if line != want[i] {
			t.Errorf("line %v = %q,\nwant %q", i, line, want[i])
		}
	}
}

// TestAddrCommandMarksTheIdentitySoItIsNotANodeLink asserts the one detail that
// makes the identity line safe to print: the client's node-link pattern refuses a
// hash that follows an "@", so an identity hash printed bare would be underlined as
// a node that does not exist, while one printed after an "@" stays plain text.
func TestAddrCommandMarksTheIdentitySoItIsNotANodeLink(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "addr me")
	identityHex := hexString(peerHashFor(0x11))

	seen := false
	for _, line := range lines {
		if !strings.Contains(line, identityHex) {
			continue
		}
		seen = true
		if !strings.Contains(line, "@"+identityHex) {
			t.Errorf("the identity hash appears without its @ sigil, so a client would linkify it as a node: %q", line)
		}
	}
	if !seen {
		t.Fatalf("addr never printed the identity hash: %q", lines)
	}

	// The message address keeps its own sigil, which is what tells a client that
	// this hash is a peer to converse with rather than a node to browse.
	wantLXMF := "lxmf@" + addrDestHex(peerHashFor(0x11), "lxmf", "delivery")
	found := false
	for _, line := range lines {
		if strings.HasPrefix(line, wantLXMF) {
			found = true
		}
	}
	if !found {
		t.Errorf("addr printed no %v line: %q", wantLXMF, lines)
	}
}

// TestAddrCommandAddsTheHubDestinationOnlyForTheHub asserts the hub-only rule the
// path command shares: a peer is not a hub, and reporting rrc.hub for one would name
// a destination that does not exist.
func TestAddrCommandAddsTheHubDestinationOnlyForTheHub(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	peer := peerHashFor(0x31)
	fake.setKnownPeer(hexString(peer), qrTestPeerNick)

	for _, line := range runLines(t, reg, session, "addr "+qrTestPeerNick) {
		if strings.Contains(line, "rrc.hub") {
			t.Fatalf("a peer was reported as a hub: %q", line)
		}
	}

	// Now make the subject the hub itself, which owns rrc.hub.
	hubIdentity := peerHashFor(0x41)
	fake.setHubIdentity(hubIdentity)
	fake.setKnownPeer(hexString(hubIdentity), "thehub")
	lines := runLines(t, reg, session, "addr thehub")
	want := addrDestHex(hubIdentity, "rrc", "hub") + " (rrc.hub, the hub itself)"
	if got := lines[len(lines)-1]; got != want {
		t.Errorf("the hub's last line = %q, want %q", got, want)
	}
	if len(lines) != 4 {
		t.Errorf("the hub reply is %v lines, want 4 (identity, lxmf, node, hub)", len(lines))
	}
}

// TestAddrCommandDefaultsToTheAsker asserts a bare addr answers about the asker,
// which is the question people actually ask, and that it stays within the four
// lines an ordinary reply may cost.
func TestAddrCommandDefaultsToTheAsker(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	bare := runLines(t, reg, session, "addr")
	named := runLines(t, reg, session, "addr me")
	if strings.Join(bare, "\n") != strings.Join(named, "\n") {
		t.Errorf("addr = %q, but addr me = %q; they must agree", bare, named)
	}
	if len(bare) > 4 {
		t.Errorf("addr replied with %v lines, want at most 4", len(bare))
	}
}

// TestAddrCommandRejectsExtraWords asserts a misuse is answered with the usage line
// rather than a guess.
func TestAddrCommandRejectsExtraWords(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "addr me lxmf")
	if len(lines) != 1 || lines[0] != "Usage: "+addrUsage {
		t.Errorf("reply = %q, want the usage line %q", lines, "Usage: "+addrUsage)
	}
}
