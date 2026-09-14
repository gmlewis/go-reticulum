// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrc

import (
	"slices"
	"testing"
	"time"
)

// TestIsEphemeralNotice pins the conversation/ephemeral split: only rows that
// are hub chatter are ephemeral, so only they may be dropped on load and aged
// out by the periodic cleanup.
func TestIsEphemeralNotice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		msg         *RRCMessage
		wantEphem   bool
		wantConvers bool
	}{
		{"nil", nil, false, false},
		{"msg", &RRCMessage{Kind: "msg", Nick: "peer"}, false, false},
		{"system", &RRCMessage{Kind: "system"}, true, false},
		{"hub notice", &RRCMessage{Kind: "notice"}, true, false},
		{"pinned motd", &RRCMessage{Kind: "notice", Pinned: true}, false, false},
		{"private notice", &RRCMessage{Kind: "notice", Nick: "gorrcbot", Direct: true}, false, true},
		{"peer notice", &RRCMessage{Kind: "notice", Nick: "gorrcbot"}, false, true},
		{"peer system", &RRCMessage{Kind: "system", Nick: "gorrcbot"}, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.msg.IsEphemeralNotice(); got != tt.wantEphem {
				t.Errorf("IsEphemeralNotice() = %v, want %v", got, tt.wantEphem)
			}
			if got := tt.msg.IsConversation(); got != tt.wantConvers {
				t.Errorf("IsConversation() = %v, want %v", got, tt.wantConvers)
			}
		})
	}
}

// TestHubBootKeepsBotAndPrivateReplies is the regression test for the live
// failure it reproduces: gonomadnet on the Mac mini was restarted, and after
// the restart the room view showed the user's own commands ("/msg gorrcbot
// help", "@gorrcbot help dn") but NEITHER gorrbot's private replies NOR its
// public reply, even though every one of them was present in the stored room
// history file.
//
// Two independent filters erased them: loadHistory dropped every non-direct
// notice (so the bot's public reply never reached the buffer), and the first
// cleanHistory call after boot purged every notice older than the
// ephemeral-notices timeout from the buffer, including the private replies
// that had just been loaded. Both filters must now keep conversation.
func TestHubBootKeepsBotAndPrivateReplies(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	mgr := NewManager(dir, nil)
	mgr.SetHistoryConfig(500, true, 600) // Python's defaults: cap 500, filter on
	hub := mgr.AddHub([]byte{0xa0, 0x12, 0x12, 0x9c}, "rrc.hub", "H")
	hub.AddRoom("general")

	// Timestamps two hours old: everything below is "stale" by the 600s
	// ephemeral timeout, exactly as a reloaded file is after a restart.
	stale := time.Now().Add(-2 * time.Hour).UnixMilli()
	hubHash := []byte("hubhash")
	botHash := []byte("bothash")

	appendHistoryEntry(t, hub, "general", &RRCMessage{Kind: "notice", Src: hubHash, Text: "Welcome! JOIN #general", Ts: stale})
	appendHistoryEntry(t, hub, "general", &RRCMessage{Kind: "system", Text: "gonomadnet on RaspPi joined", Ts: stale})
	appendHistoryEntry(t, hub, "general", &RRCMessage{Kind: "msg", Src: []byte("ownhash"), Nick: "glenn", Text: "/msg gorrcbot help", Ts: stale})
	appendHistoryEntry(t, hub, "general", &RRCMessage{Kind: "notice", Src: hubHash, Text: "Direct NOTICE sent to ff2fbcf2e5e57ef710e4b3a855482390", Ts: stale})
	appendHistoryEntry(t, hub, "general", &RRCMessage{Kind: "notice", Src: botHash, Nick: "gorrcbot", Direct: true, Dst: []byte("ownhash"), Text: "Commands: botinfo, help, id", Ts: stale})
	appendHistoryEntry(t, hub, "general", &RRCMessage{Kind: "msg", Src: []byte("ownhash"), Nick: "glenn", Text: "@gorrcbot help dn", Ts: stale})
	appendHistoryEntry(t, hub, "general", &RRCMessage{Kind: "notice", Src: botHash, Nick: "gorrcbot", Text: "dn — send a direct NOTICE to one client.", Ts: stale})

	hub.loadHistory()
	// The first live row after a reconnect triggers the cleanup, which used to
	// erase the replies that loadHistory had just restored.
	hub.lastHistoryClean.Store(0)
	hub.cleanHistory()

	hub.lock.Lock()
	msgs := hub.Messages["general"]
	hub.lock.Unlock()

	want := []string{
		"/msg gorrcbot help",
		"Commands: botinfo, help, id",
		"@gorrcbot help dn",
		"dn — send a direct NOTICE to one client.",
	}
	if got := messageTexts(msgs); !slices.Equal(got, want) {
		t.Fatalf("room buffer after boot = %v, want %v", got, want)
	}
	// The private reply must still render as private, the public one as a
	// plain peer notice.
	if !msgs[1].Direct || msgs[1].Nick != "gorrcbot" {
		t.Errorf("private reply = %+v, want Direct with nick gorrbot", msgs[1])
	}
	if msgs[3].Direct {
		t.Errorf("public bot reply marked Direct: %+v", msgs[3])
	}
}

// TestHubCleanHistoryKeepsConversation verifies the periodic purge ages out
// hub chatter while leaving conversation in place, however old it is.
func TestHubCleanHistoryKeepsConversation(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	mgr := NewManager(dir, nil)
	mgr.SetHistoryConfig(0, true, 600)
	hub := mgr.AddHub([]byte{0x01}, "rrc.hub", "H")

	now := time.Now().Unix()
	old := (now - 700) * 1000
	hub.lastHistoryClean.Store(0)

	hub.lock.Lock()
	hub.Messages["general"] = []*RRCMessage{
		{Kind: "msg", Room: "general", Text: "keep-msg", Ts: old},
		{Kind: "notice", Room: "general", Text: "old-hub-notice", Ts: old},
		{Kind: "system", Room: "general", Text: "old-system", Ts: old},
		{Kind: "notice", Room: "general", Nick: "gorrcbot", Text: "old-bot-reply", Ts: old},
		{Kind: "notice", Room: "general", Nick: "gorrcbot", Direct: true, Text: "old-private-reply", Ts: old},
		{Kind: "notice", Room: "general", Pinned: true, Text: "old-pinned-motd", Ts: old},
		{Kind: "notice", Room: "general", Text: "fresh-hub-notice", Ts: now * 1000},
	}
	hub.lock.Unlock()

	hub.cleanHistory()

	hub.lock.Lock()
	defer hub.lock.Unlock()

	if got := messageTexts(hub.Messages["general"]); !slices.Equal(got, []string{
		"keep-msg", "old-bot-reply", "old-private-reply", "old-pinned-motd", "fresh-hub-notice",
	}) {
		t.Fatalf("buffer after cleanup = %v", got)
	}
	if hub.cleanLastRemoved.Load() == 0 {
		t.Error("cleanLastRemoved not set after a cleanup that removed hub chatter")
	}
}
