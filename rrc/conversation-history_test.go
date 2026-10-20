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

// TestHubBootKeepsBotAndPrivateReplies asserts that a restart keeps bot
// conversation: after loadHistory and the first cleanHistory call, the user's
// commands, the bot's public reply, and its private replies all remain in the
// room buffer even though every row is older than the ephemeral-notices
// timeout. Two filters must keep conversation: loadHistory must not drop peer
// notices, and cleanHistory must not age out private or peer notices.
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
	// The first live row after a reconnect triggers the cleanup; it must not
	// erase the replies loadHistory just restored.
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
