// Copyright 2026 Glenn Lewis. All rights reserved.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package rrc

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

// TestAPrivateReplyBeginningWithASlashReachesItsPeer is the regression test for the defect
// behind `/msg gobot ...` showing nothing at all whenever the bot had no fix.
//
// The hub intercepts any MSG or NOTICE whose text starts with a slash, and answers it as a
// command for itself. That interception ran before the direct-notice dispatch and never looked
// at K_DST, so a NOTICE naming another peer — somebody else's mail, which the hub is merely
// carrying — was read as a command for the hub and swallowed.
//
// The bot's own answer to a request it cannot fulfil is exactly that shape:
//
//	/whereami: no GNSS fix yet — acquiring; give a location to answer immediately (...)
//
// so the asker waited out its whole timeout in silence while the hub logged "Slash command" and
// went about its business. It cost three rounds of investigation, two of which blamed a bot
// restart and a client reconnect that had nothing to do with it.
func TestAPrivateReplyBeginningWithASlashReachesItsPeer(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	env.setDestination(t)

	botHash := bytesOf(0x11, 32)
	peerHash := bytesOf(0x22, 32)
	botLink := &rns.Link{}
	peerLink := &rns.Link{}

	env.identifyLink(t, botLink, botHash)
	env.helloFrom(botLink, botHash, "gobot")
	env.identifyLink(t, peerLink, peerHash)
	env.helloFrom(peerLink, peerHash, "probe")

	const noFix = "/whereami: no GNSS fix yet — acquiring; give a location to answer immediately"
	env.sendAs(botLink, directNoticeTo(botHash, peerHash, noFix))

	links := env.sendLinks()
	if len(links) != 1 {
		t.Fatalf("the reply produced %v send(s), want exactly one, to the peer it was addressed "+
			"to: %v", len(links), links)
	}
	if links[0] != peerLink {
		t.Fatalf("the reply went to %v, want the peer's link %v", links[0], peerLink)
	}

	// It must arrive as the text the bot wrote. An operator-command reply, or an error about an
	// unrecognized command, would also be a single send — and would still leave the asker
	// without the sentence the bot was trying to say.
	sends := env.decodedSends(t)
	body, _ := sends[0].Get(KBody)
	if got, isStr := body.(string); !isStr || got != noFix {
		t.Fatalf("the delivered body = %#v, want the bot's own sentence", body)
	}
	if got := envType(t, sends[0]); got == TError {
		t.Fatal("the reply was delivered as an error envelope")
	}
}

// TestACommandAddressedToTheHubIsStillIntercepted is the other side of the rule, so the fix
// cannot be "stop intercepting": a NOTICE naming the hub's own identity is a command for the
// hub and must still be handled as one, not forwarded to anybody.
func TestACommandAddressedToTheHubIsStillIntercepted(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	env.setDestination(t)

	hubHash := env.hub.DestinationHash()
	if len(hubHash) == 0 {
		t.Fatal("the test hub has no destination identity")
	}

	senderHash := bytesOf(0x33, 32)
	senderLink := &rns.Link{}
	spectator := &rns.Link{}

	env.identifyLink(t, senderLink, senderHash)
	env.helloFrom(senderLink, senderHash, "probe")
	env.identifyLink(t, spectator, bytesOf(0x44, 32))
	env.helloFrom(spectator, bytesOf(0x44, 32), "other")

	env.sendAs(senderLink, directNoticeTo(senderHash, hubHash, "/whereami"))

	// It is answered on the sender's own link, and goes no further.
	for _, link := range env.sendLinks() {
		if link == spectator {
			t.Fatal("a command addressed to the hub was forwarded to another peer as a message")
		}
	}
}
