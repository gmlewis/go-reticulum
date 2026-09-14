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
	"encoding/hex"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// directNoticeEnvelope builds the exact envelope the hub's handleDirectNotice
// forwards to a target link: a NOTICE with K_DST set to the target identity
// hash, K_SRC rewritten to the REQUESTER's hash, the requester's nick adopted,
// and no K_ROOM.
func directNoticeEnvelope(target, requester []byte, body string) *cbor.Map {
	env := MakeEnvelope(int(TNotice), requester, WithBody(body), WithID(make([]byte, 8)))
	env.Set(KDst, target)
	env.Set(KNick, "Requester")
	return env
}

// TestHandleDataMarksInboundDirectNotice asserts a NOTICE carrying K_DST is
// surfaced as a direct notice with the body, nick, and source intact.
func TestHandleDataMarksInboundDirectNotice(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	got := make(chan *RRCMessage, 4)
	hub.SetOnMessage(func(msg *RRCMessage) { got <- msg })

	target := hub.Manager.identityHash()
	requester := peerHash(0x20)
	env := directNoticeEnvelope(target, requester, "help")
	hub.HandleData(cbor.Encode(env))

	var msg *RRCMessage
	select {
	case msg = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the direct notice to reach the hook")
	}
	if !msg.Direct {
		t.Error("Direct = false, want true for a NOTICE carrying K_DST")
	}
	if hex.EncodeToString(msg.Dst) != hex.EncodeToString(target) {
		t.Errorf("Dst = %v, want %v", hex.EncodeToString(msg.Dst), hex.EncodeToString(target))
	}
	if hex.EncodeToString(msg.Src) != hex.EncodeToString(requester) {
		t.Errorf("Src = %v, want the requester %v", hex.EncodeToString(msg.Src), hex.EncodeToString(requester))
	}
	if msg.Nick != "Requester" {
		t.Errorf("Nick = %q, want %q", msg.Nick, "Requester")
	}
	if msg.Text != "help" {
		t.Errorf("Text = %q, want %q", msg.Text, "help")
	}
	if msg.Kind != "notice" {
		t.Errorf("Kind = %q, want %q", msg.Kind, "notice")
	}
}

// TestHandleDataDirectNoticeDoesNotBecomeMOTD asserts a private notice is not
// mistaken for the hub's greeting: only the hub's roomless broadcast sets the
// MOTD, and a direct notice is not pinned against the ephemeral purge.
func TestHandleDataDirectNoticeDoesNotBecomeMOTD(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	hub.SetMOTD("Welcome to the hub")

	target := hub.Manager.identityHash()
	env := directNoticeEnvelope(target, peerHash(0x20), "private pong")
	hub.HandleData(cbor.Encode(env))

	if got := hub.GetMOTD(); got != "Welcome to the hub" {
		t.Errorf("MOTD = %q, want the hub greeting unchanged", got)
	}
	hub.lock.Lock()
	notices := append([]*RRCMessage(nil), hub.Notices...)
	hub.lock.Unlock()
	for _, n := range notices {
		if n.Text == "private pong" && n.Pinned {
			t.Error("a direct notice was recorded as a pinned (greeting) notice")
		}
	}
}

// TestHandleDataRoomlessNoticeWithoutDstIsNotDirect asserts the K_DST key is
// what marks a notice direct — a plain roomless notice is not.
func TestHandleDataRoomlessNoticeWithoutDstIsNotDirect(t *testing.T) {
	t.Parallel()

	_, hub := newHookTestHub(t)
	got := make(chan *RRCMessage, 4)
	hub.SetOnMessage(func(msg *RRCMessage) { got <- msg })

	env := MakeEnvelope(int(TNotice), peerHash(0x30), WithBody("hub greeting"), WithID(make([]byte, 8)))
	hub.HandleData(cbor.Encode(env))

	var msg *RRCMessage
	select {
	case msg = <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the roomless notice")
	}
	if msg.Direct {
		t.Error("Direct = true for a NOTICE without K_DST")
	}
	if len(msg.Dst) != 0 {
		t.Errorf("Dst = %v, want empty for a NOTICE without K_DST", hex.EncodeToString(msg.Dst))
	}
	if hub.GetMOTD() != "hub greeting" {
		t.Errorf("MOTD = %q, want the roomless notice's text", hub.GetMOTD())
	}
}

// TestDirectNoticeHistoryRoundTrip asserts Direct and Dst survive the history
// file round trip, mirroring how Mention persists via HMention.
func TestDirectNoticeHistoryRoundTrip(t *testing.T) {
	t.Parallel()

	target := peerHash(0x70)
	msg := &RRCMessage{
		Kind:   "notice",
		Src:    peerHash(0x71),
		Nick:   "Requester",
		Text:   "help",
		Ts:     1234567890,
		Direct: true,
		Dst:    target,
	}

	entry := msg.HistoryEntry()
	if entry[HDirect] != true {
		t.Errorf("entry[HDirect] = %v, want true", entry[HDirect])
	}
	if got, ok := entry[HDst].([]byte); !ok || hex.EncodeToString(got) != hex.EncodeToString(target) {
		t.Errorf("entry[HDst] = %v, want %v", entry[HDst], hex.EncodeToString(target))
	}

	back := DecodeHistoryEntry(entry)
	if !back.Direct {
		t.Error("round-tripped Direct = false, want true")
	}
	if hex.EncodeToString(back.Dst) != hex.EncodeToString(target) {
		t.Errorf("round-tripped Dst = %v, want %v", hex.EncodeToString(back.Dst), hex.EncodeToString(target))
	}
	if back.Text != "help" || back.Nick != "Requester" {
		t.Errorf("round-tripped Text/Nick = %q/%q, want %q/%q", back.Text, back.Nick, "help", "Requester")
	}
}

// TestHistoryEntryOmitsDirectKeysForPlainMessages asserts a normal message's
// history entry carries no direct-notice keys, keeping existing history files
// byte-identical.
func TestHistoryEntryOmitsDirectKeysForPlainMessages(t *testing.T) {
	t.Parallel()

	entry := (&RRCMessage{Kind: "msg", Text: "hi", Ts: 1}).HistoryEntry()
	if _, ok := entry[HDirect]; ok {
		t.Error("plain message history entry carries HDirect")
	}
	if _, ok := entry[HDst]; ok {
		t.Error("plain message history entry carries HDst")
	}
}
