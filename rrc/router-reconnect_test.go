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
	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// directNoticeTo builds the envelope a bot sends when it answers a peer privately: a NOTICE
// with no room, whose destination names the peer's identity.
func directNoticeTo(src, dst []byte, text string) *cbor.Map {
	return MakeEnvelope(int(TNotice), src, WithNick("gobot"), WithDst(dst), WithBody(text))
}

// TestADirectNoticeReachesAPeerThatReconnected is the regression test for a defect that made
// `/msg gobot ...` work exactly once per hub run.
//
// A client that reconnects gets a new link for the same identity. The hub indexes peers by
// identity so it can route a private reply to whichever link that identity currently holds, and
// the whole feature depends on that index surviving the reconnect. When it does not, the notice
// is dropped with nothing but a log line, and from the outside the bot looks like it stopped
// answering — which is what two rounds of investigation into the bot and its FIFOs assumed.
//
// The peer connects, leaves, and comes back on a different link before the reply is sent.
func TestADirectNoticeReachesAPeerThatReconnected(t *testing.T) {
	t.Parallel()

	env := newHubTestEnv(t)
	env.setDestination(t)

	botHash := bytesOf(0x11, 32)
	peerHash := bytesOf(0x22, 32)
	botLink := &rns.Link{}
	first := &rns.Link{}
	second := &rns.Link{}

	env.identifyLink(t, botLink, botHash)
	env.helloFrom(botLink, botHash, "gobot")

	// The peer arrives, then leaves.
	env.identifyLink(t, first, peerHash)
	env.helloFrom(first, peerHash, "probe")
	env.hub.OnClose(first)

	// And comes back on a new link.
	env.identifyLink(t, second, peerHash)
	env.helloFrom(second, peerHash, "probe")

	env.sendAs(botLink, directNoticeTo(botHash, peerHash, "whereami"))

	links := env.sendLinks()
	if len(links) != 1 {
		t.Fatalf("the reply produced %v send(s), want exactly one, to the peer's current link: %v",
			len(links), links)
	}
	if links[0] != second {
		if links[0] == first {
			t.Fatal("the reply went to the peer's PREVIOUS link. A client that reconnects holds " +
				"that link no longer, so it never sees the answer, and `/msg gobot` silently " +
				"stops working for it until the hub restarts")
		}
		t.Fatalf("the reply went to %v, want the peer's current link %v", links[0], second)
	}

	// And it is a real reply, not an error about the destination.
	for _, m := range env.decodedSends(t) {
		if got := envType(t, m); got == TError {
			t.Fatalf("the reply came back as an error envelope: %v", m)
		}
	}
}

// TestADirectNoticeReachesAPeerThatNeverLeft is the ordinary case beside it, so the test above
// cannot pass by accident: routing to an identity that holds its first and only link must work.
func TestADirectNoticeReachesAPeerThatNeverLeft(t *testing.T) {
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

	env.sendAs(botLink, directNoticeTo(botHash, peerHash, "whereami"))

	links := env.sendLinks()
	if len(links) != 1 || links[0] != peerLink {
		t.Fatalf("the reply went to %v, want the peer's link %v", links, peerLink)
	}
}
