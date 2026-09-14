// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// nickOverrideFixture returns a hub that already knows the local user as a
// member of "general" under an old nick, plus one other member.
func nickOverrideFixture(t *testing.T) (*RRCManager, *RRCHub, string, string) {
	t.Helper()
	ownHex := hexString(bytesOf(0x11, IdentityHashLen))
	peerHex := hexString(bytesOf(0x22, IdentityHashLen))

	mgr := NewManager(tempDir(t), func() []byte { return bytesOf(0x11, IdentityHashLen) })
	mgr.SetNickname("globalnick")
	hub := mgr.AddHub([]byte{0x09}, "rrc.hub", "NickHub")

	hub.lock.Lock()
	hub.Members["general"] = map[string]bool{ownHex: true, peerHex: true}
	hub.Nicks[ownHex] = "oldnick"
	hub.Nicks[peerHex] = "peer"
	hub.lock.Unlock()
	return mgr, hub, ownHex, peerHex
}

// nickOf returns the display name the Users pane would render for one member.
func nickOf(hub *RRCHub, room, hashHex string) string {
	for _, m := range hub.GetRoomMembers(room) {
		if m.HashHex == hashHex {
			return m.Nick
		}
	}
	return ""
}

// TestSetNickOverrideUpdatesTheUsersPane pins the immediate feedback a /nick
// change owes the user: the Users pane renders every member through the
// learned nick table, and the hub only learns our nick from our next message,
// so without this the user's own row kept the previous name until they spoke
// again.
func TestSetNickOverrideUpdatesTheUsersPane(t *testing.T) {
	t.Parallel()

	mgr, hub, ownHex, peerHex := nickOverrideFixture(t)
	if got := nickOf(hub, "general", ownHex); got != "oldnick" {
		t.Fatalf("test setup: own row = %q, want the previously learned nick", got)
	}

	hub.SetNickOverride("glenn")
	if got := nickOf(hub, "general", ownHex); got != "glenn" {
		t.Errorf("own row after /nick = %q, want %q", got, "glenn")
	}
	if got := nickOf(hub, "general", peerHex); got != "peer" {
		t.Errorf("another member's row = %q, want it untouched", got)
	}
	if got := hub.GetEffectiveNick(); got != "glenn" {
		t.Errorf("GetEffectiveNick() = %q, want the override", got)
	}

	// Clearing the override falls back to the manager's nickname, so the row
	// follows the effective name rather than keeping the stale override.
	hub.SetNickOverride("")
	if got := nickOf(hub, "general", ownHex); got != "globalnick" {
		t.Errorf("own row after clearing the override = %q, want %q", got, "globalnick")
	}

	// With no fallback name to show, the row falls back to the hash prefix
	// instead of a name that is no longer ours.
	mgr.SetNickname("")
	hub.SetNickOverride("")
	if got := nickOf(hub, "general", ownHex); got == "globalnick" || got == "glenn" {
		t.Errorf("own row with no effective nick = %q, want the hash prefix", got)
	}
}

// TestWhoReplyDoesNotUndoALocalNickChange asserts the hub's own listing of us
// cannot flip the Users pane back after a /nick: a /who reply carries the nick
// the hub registered, which stays the previous one until our next message
// carries the new one, and our own name is local knowledge.
func TestWhoReplyDoesNotUndoALocalNickChange(t *testing.T) {
	t.Parallel()

	_, hub, _ := reconcileFixture(t)
	hub.AddRoom("general")
	ownHex := hexString([]byte("ownhash"))
	hub.lock.Lock()
	hub.Members["general"] = map[string]bool{ownHex: true}
	hub.lock.Unlock()

	hub.SetNickOverride("glenn")
	if got := nickOf(hub, "general", ownHex); got != "glenn" {
		t.Fatalf("test setup: own row = %q, want the override", got)
	}

	// The hub still has us registered under the previous nick.
	text := "members in general: Glenn (" + ownHex[:12] + "), Bob (aabbccddeeff)"
	env := MakeClientEnvelope(TypeNotice, []byte("hubhash"), nil, []byte("hub"), text, make([]byte, 8), NowMs())
	hub.HandleData(cbor.Encode(env))

	if got := nickOf(hub, "general", ownHex); got != "glenn" {
		t.Errorf("own row after a /who reply = %q, want the override to survive", got)
	}
	// Another member listed in the same reply is still learned normally.
	peerHex := ""
	for _, m := range hub.GetRoomMembers("general") {
		if m.HashHex != ownHex {
			peerHex = m.HashHex
		}
	}
	if peerHex == "" {
		t.Fatal("the other member of the /who reply is missing")
	}
	if got := nickOf(hub, "general", peerHex); got != "Bob" {
		t.Errorf("another member's row = %q, want %q", got, "Bob")
	}
}
