// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rrc"
)

const (
	// triggerNicks is the identity hash the trigger tests run as.
	triggerOwnHash = "a012129c10205c0b9441fcd2b755b2a7"
	// triggerNick is the nick the trigger tests answer to.
	triggerNick = "gorrcbot"
)

// TestParseTriggerAddressed covers every text shape that must address the bot.
func TestParseTriggerAddressed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		wantCmd string
	}{
		{name: "bare nick", text: "@gorrcbot", wantCmd: ""},
		{name: "nick and command", text: "@gorrcbot help", wantCmd: "help"},
		{name: "uppercase nick", text: "@GORRCBOT help", wantCmd: "help"},
		{name: "mixed case nick", text: "@GoRrCbOt ping", wantCmd: "ping"},
		{name: "trailing colon", text: "@gorrcbot: help", wantCmd: "help"},
		{name: "trailing comma", text: "@gorrcbot, help", wantCmd: "help"},
		{name: "trailing colon no space", text: "@gorrcbot:help", wantCmd: "help"},
		{name: "trailing comma and command only", text: "@gorrcbot,ping", wantCmd: "ping"},
		{name: "leading whitespace", text: "   @gorrcbot help", wantCmd: "help"},
		{name: "hash prefix", text: "@a012129c help", wantCmd: "help"},
		{name: "hash prefix upper case", text: "@A012129C help", wantCmd: "help"},
		{name: "longer hash prefix", text: "@a012129c10205c0b help", wantCmd: "help"},
		{name: "full hash", text: "@" + triggerOwnHash + " help", wantCmd: "help"},
		{name: "multi word command", text: "@gorrcbot dnotice alice hello there", wantCmd: "dnotice alice hello there"},
		{name: "command with extra spaces", text: "@gorrcbot    help   ", wantCmd: "help"},
		{name: "unicode command", text: "@gorrcbot wx Zürich", wantCmd: "wx Zürich"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := &rrc.RRCMessage{Kind: "msg", Room: "general", Nick: "Alice",
				Src: peerHashFor(0x11), Text: tt.text}
			got := parseTrigger(msg, triggerNick, triggerOwnHash)
			if !got.Addressed {
				t.Fatalf("parseTrigger(%q).Addressed = false, want true", tt.text)
			}
			if got.Command != tt.wantCmd {
				t.Errorf("Command = %q, want %q", got.Command, tt.wantCmd)
			}
			if got.Direct {
				t.Error("Direct = true for a room message, want false")
			}
			if got.Nick != triggerNick {
				t.Errorf("Nick = %q, want %q", got.Nick, triggerNick)
			}
		})
	}
}

// TestParseTriggerHashPrefixBoundaries asserts the hash alias needs a long
// enough prefix and the right identity.
func TestParseTriggerHashPrefixBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "five hex characters is too short", text: "@a0121 help", want: false},
		{name: "six hex characters is enough", text: "@a01212 help", want: true},
		{name: "wrong identity", text: "@ffffff help", want: false},
		{name: "nick-looking hex that is not ours", text: "@be1e7400 help", want: false},
		{name: "hex longer than ours", text: "@a012129c10205c0b9441fcd2b755b2a7ff help", want: false},
		{name: "non hex of the right length", text: "@zzzzzz help", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := &rrc.RRCMessage{Kind: "msg", Room: "general", Text: tt.text}
			if got := parseTrigger(msg, triggerNick, triggerOwnHash).Addressed; got != tt.want {
				t.Errorf("parseTrigger(%q).Addressed = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

// TestParseTriggerMustNotTrigger covers the silence contract: gorrcbot answers
// only when it is addressed, so anything else must produce nothing at all.
func TestParseTriggerMustNotTrigger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  *rrc.RRCMessage
	}{
		{name: "bang help", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "!help"}},
		{name: "bang prefix with nick", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "!gorrcbot help"}},
		{name: "mention mid line", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "hi @gorrcbot how are you"}},
		{name: "nick alone in prose", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "gorrcbot is down"}},
		{name: "someone else addressed", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "@someoneelse help"}},
		{name: "no word boundary", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "@gorrcbotx help"}},
		{name: "nick is a prefix of ours", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "@gorrc help"}},
		{name: "quoted mid line", msg: &rrc.RRCMessage{Kind: "msg", Room: "general",
			Text: `then send "@gorrcbot help" to the room`}},
		{name: "at sign alone", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "@"}},
		{name: "at sign then space", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "@ help"}},
		{name: "empty text", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: ""}},
		{name: "email-like", msg: &rrc.RRCMessage{Kind: "msg", Room: "general", Text: "mail gorrcbot@example.com"}},
		{name: "room notice", msg: &rrc.RRCMessage{Kind: "notice", Room: "general", Text: "@gorrcbot help"}},
		{name: "system row", msg: &rrc.RRCMessage{Kind: "system", Room: "general", Text: "@gorrcbot help"}},
		{name: "error row", msg: &rrc.RRCMessage{Kind: "error", Room: "general", Text: "@gorrcbot help"}},
		{name: "nil message", msg: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parseTrigger(tt.msg, triggerNick, triggerOwnHash).Addressed; got {
				t.Errorf("parseTrigger(%+v).Addressed = true, want false", tt.msg)
			}
		})
	}
}

// TestParseTriggerActionIsAddressed asserts a /me action can address the bot.
func TestParseTriggerActionIsAddressed(t *testing.T) {
	t.Parallel()

	msg := &rrc.RRCMessage{Kind: "action", Room: "general", Nick: "Alice", Text: "@gorrcbot ping"}
	got := parseTrigger(msg, triggerNick, triggerOwnHash)
	if !got.Addressed || got.Command != "ping" {
		t.Errorf("parseTrigger(action) = %+v, want addressed with command ping", got)
	}
}

// TestParseTriggerDirectNoticeIsAFullCommand asserts a direct NOTICE addressed
// to the bot needs no @ prefix: the address is the envelope itself.
func TestParseTriggerDirectNoticeIsAFullCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		wantCmd string
	}{
		{name: "bare command", text: "help", wantCmd: "help"},
		{name: "command with argument", text: "weather London", wantCmd: "weather London"},
		{name: "padded", text: "   ping   ", wantCmd: "ping"},
		{name: "at prefix still accepted", text: "@gorrcbot help", wantCmd: "@gorrcbot help"},
		{name: "empty", text: "  ", wantCmd: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := &rrc.RRCMessage{Kind: "notice", Nick: "Alice", Src: peerHashFor(0x11),
				Direct: true, Dst: peerHashFor(0x22), Text: tt.text}
			got := parseTrigger(msg, triggerNick, triggerOwnHash)
			if !got.Addressed {
				t.Fatalf("parseTrigger(direct %q).Addressed = false, want true", tt.text)
			}
			if !got.Direct {
				t.Error("Direct = false for a direct notice, want true")
			}
			if got.Command != tt.wantCmd {
				t.Errorf("Command = %q, want %q", got.Command, tt.wantCmd)
			}
		})
	}
}

// TestParseTriggerPerRoomNick asserts respond_to overrides change which nick is
// accepted in that room.
func TestParseTriggerPerRoomNick(t *testing.T) {
	t.Parallel()

	msg := &rrc.RRCMessage{Kind: "msg", Room: "ops", Text: "@opsbot dn alice hi"}
	got := parseTrigger(msg, "opsbot", triggerOwnHash)
	if !got.Addressed || got.Command != "dn alice hi" {
		t.Fatalf("parseTrigger(@opsbot) = %+v, want addressed with the ops command", got)
	}
	if got := parseTrigger(msg, triggerNick, triggerOwnHash); got.Addressed {
		t.Errorf("parseTrigger(@opsbot, nick=%q).Addressed = true, want false in that room", triggerNick)
	}
	if got := parseTrigger(&rrc.RRCMessage{Kind: "msg", Room: "ops", Text: "@gorrcbot help"},
		"opsbot", triggerOwnHash); got.Addressed {
		t.Error("the global nick triggered in a room that overrides it, want silence")
	}
}

// TestParseTriggerEmptyNickNeverMatches asserts a missing nick cannot turn an
// empty token into a match.
func TestParseTriggerEmptyNickNeverMatches(t *testing.T) {
	t.Parallel()

	if got := parseTrigger(&rrc.RRCMessage{Kind: "msg", Room: "general", Text: "@ help"}, "", triggerOwnHash); got.Addressed {
		t.Error("an empty nick matched the @ token, want silence")
	}
	if got := parseTrigger(&rrc.RRCMessage{Kind: "msg", Room: "general", Text: "@ help"}, "   ", triggerOwnHash); got.Addressed {
		t.Error("a whitespace nick matched the @ token, want silence")
	}
}

// TestParseTriggerHashPrefixLength asserts the documented minimum prefix.
func TestParseTriggerHashPrefixLength(t *testing.T) {
	t.Parallel()

	if MinHashPrefix != 6 {
		t.Errorf("MinHashPrefix = %v, want 6", MinHashPrefix)
	}
}
