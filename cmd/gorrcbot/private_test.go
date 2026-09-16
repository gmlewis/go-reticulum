// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file guards the rule that no command may copy a private row into a room
// answer. The RRC client files an inbound direct notice in the room's buffer and
// history (its UI shows it as "private from <nick>"), and the persisted entry
// keeps only the destination hash, so a reader that trusts the pinned flag alone
// republishes private text.

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

const (
	// privateCanary is the text of a private command line, unique enough that a
	// reply quoting it can only have come from the row under test.
	privateCanary = "msg glenn SECRETBODY7391"
	// publicLine is room conversation that a reader may quote freely.
	publicLine = "the link is at nomadnet://example"
)

// TestPrivateRowRecognizesEveryPrivacyMarker pins the predicate itself: each
// privacy marker on its own is enough, and ordinary room traffic is not private.
func TestPrivateRowRecognizesEveryPrivacyMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  *rrc.RRCMessage
		want bool
	}{
		{name: "nil", row: nil, want: true},
		{name: "pinned", row: &rrc.RRCMessage{Kind: "notice", Pinned: true}, want: true},
		{name: "direct", row: &rrc.RRCMessage{Kind: "notice", Direct: true}, want: true},
		{name: "destination as persisted", row: &rrc.RRCMessage{Kind: "notice", Dst: []byte{1, 2, 3}}, want: true},
		{name: "a room message", row: &rrc.RRCMessage{Kind: "msg", Nick: "Dave", Text: publicLine}},
		{name: "a room notice", row: &rrc.RRCMessage{Kind: "notice", Nick: "gorrcbot", Text: "pong"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := privateRow(tt.row); got != tt.want {
				t.Errorf("privateRow(%v) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

// seedLive puts rows straight into the fake hub's room buffers, which is what the
// client's own record path leaves behind. It does not go through deliver: the
// command fixture registers no inbound hook, so that helper is a no-op here.
func seedLive(t *testing.T, fake *fakeHub, rows ...*rrc.RRCMessage) {
	t.Helper()
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, row := range rows {
		fake.messages[row.Room] = append(fake.messages[row.Room], row)
	}
}

// privateFixture builds a session whose live buffer and history each hold the
// private canary and the public line, in the shapes the client really produces.
func privateFixture(t *testing.T) (*registry, *hubSession, *fakeHub) {
	t.Helper()

	cfg := defaultTestConfig()
	cfg.StorageDir = tempDir(t)
	reg, session, fake := commandFixture(t, cfg)
	asker := peerHashFor(0x11)
	botHash := mustHex(replyOwnHash)
	base := time.Now().UnixMilli()

	// The live buffer row carries every marker, exactly as recordDirectNotice
	// leaves it.
	seedLive(t, fake,
		&rrc.RRCMessage{
			Kind: "notice", Room: "general", Src: asker, Nick: "Dave", Text: privateCanary,
			Pinned: true, Direct: true, Dst: botHash, Ts: base,
		},
		&rrc.RRCMessage{
			Kind: "msg", Room: "general", Src: asker, Nick: "Dave", Text: publicLine,
			Ts: base,
		},
	)
	// The persisted row is what the file really holds: K_DST support writes the
	// direct flag and the destination together, and the pinned flag is never
	// written at all.
	writeHistoryDir(t, cfg.StorageDir, fakeHubOne, map[string][]*rrc.RRCMessage{
		"general": {{
			Kind: "notice", Room: "general", Src: asker, Nick: "Dave", Text: privateCanary,
			Direct: true, Dst: botHash, Ts: base,
		}},
	})
	return reg, session, fake
}

// TestSearchNeverQuotesAPrivateCommand asserts the search answer cannot contain
// private text from either the live buffer or the persisted history, while room
// conversation still matches.
func TestSearchNeverQuotesAPrivateCommand(t *testing.T) {
	t.Parallel()

	reg, session, _ := privateFixture(t)

	lines := runLines(t, reg, session, "search SECRETBODY7391")
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, privateCanary) {
		t.Errorf("search quoted the private command line: %v", lines)
	}
	if !strings.Contains(joined, "no messages match") {
		t.Errorf("search = %v, want no match for the private text", lines)
	}

	lines = runLines(t, reg, session, "search nomadnet://example")
	if !strings.Contains(strings.Join(lines, "\n"), publicLine) {
		t.Errorf("search = %v, want the public room line quoted", lines)
	}
}

// TestCatchupNeverDigestsAPrivateCommand asserts the digest cannot contain
// private text from either source, while room conversation is digested.
func TestCatchupNeverDigestsAPrivateCommand(t *testing.T) {
	t.Parallel()

	reg, session, _ := privateFixture(t)

	lines := runLines(t, reg, session, "catchup")
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, privateCanary) {
		t.Errorf("catchup quoted the private command line: %v", lines)
	}
	if !strings.Contains(joined, publicLine) {
		t.Errorf("catchup = %v, want the public room line digested", lines)
	}
}

// TestSeenNeverQuotesAPrivateCommand asserts a client's private command line is
// not reported as their last remark: the answer may name them, but not quote
// private text.
func TestSeenNeverQuotesAPrivateCommand(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	reg, session, fake := commandFixture(t, cfg)
	asker := peerHashFor(0x11)
	fake.setKnownPeer(hexString(asker), "Dave")
	seedLive(t, fake, &rrc.RRCMessage{
		Kind: "notice", Room: "general", Src: asker, Nick: "Dave", Text: privateCanary,
		Pinned: true, Direct: true, Dst: mustHex(replyOwnHash), Ts: time.Now().UnixMilli(),
	})

	lines := runLines(t, reg, session, "seen Dave")
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, privateCanary) {
		t.Errorf("seen quoted the private command line: %v", lines)
	}
	if !strings.Contains(joined, "no messages from") {
		t.Errorf("seen = %v, want no public message from Dave", lines)
	}

	// A real room remark is still reported, so the filter is not just silencing
	// the command.
	seedLive(t, fake, &rrc.RRCMessage{
		Kind: "msg", Room: "general", Src: asker, Nick: "Dave", Text: publicLine,
		Ts: time.Now().UnixMilli(),
	})
	lines = runLines(t, reg, session, "seen Dave")
	if !strings.Contains(strings.Join(lines, "\n"), publicLine) {
		t.Errorf("seen = %v, want Dave's room message quoted", lines)
	}
}

// TestSeenReadsThePersistedHistory asserts seen answers after a restart too: a
// row that only exists in the history file still counts as something the client
// said, which is what catchup and search already do.
func TestSeenReadsThePersistedHistory(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.StorageDir = tempDir(t)
	reg, session, fake := commandFixture(t, cfg)
	asker := peerHashFor(0x11)
	fake.setKnownPeer(hexString(asker), "Dave")
	writeHistoryDir(t, cfg.StorageDir, fakeHubOne, map[string][]*rrc.RRCMessage{
		"general": {{
			Kind: "msg", Room: "general", Src: asker, Nick: "Dave", Text: publicLine,
			Ts: time.Now().Add(-time.Hour).UnixMilli(),
		}},
	})

	lines := runLines(t, reg, session, "seen Dave")
	if !strings.Contains(strings.Join(lines, "\n"), publicLine) {
		t.Errorf("seen = %v, want the history row reported", lines)
	}
}

// TestPrivateRowsSurviveOnDisk asserts the fixture's persisted shape matches the
// real file, so the tests above exercise the leak rather than a stand-in: the
// direct flag and the destination together survive the round trip, and the
// pinned flag does not.
func TestPrivateRowsSurviveOnDisk(t *testing.T) {
	t.Parallel()

	storage := tempDir(t)
	dir := writeHistoryDir(t, storage, fakeHubOne, map[string][]*rrc.RRCMessage{
		"general": {{
			Kind: "notice", Room: "general", Src: peerHashFor(0x11), Nick: "Dave",
			Text: privateCanary, Direct: true, Dst: mustHex(replyOwnHash),
			Ts: time.Now().UnixMilli(),
		}},
	})
	path := dir + "/" + normalizeRoom("general") + "_" + historySuffix("general") + ".log"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), privateCanary) {
		t.Errorf("the history file does not hold the row: %q", data)
	}
	store := newHistoryStore(storage, fakeHubOne)
	rows := store.newest("general", 10)
	if len(rows) != 1 {
		t.Fatalf("read back %v rows, want 1", len(rows))
	}
	if !privateRow(rows[0]) {
		t.Errorf("the row read back from disk is not recognized as private: %+v", rows[0])
	}
}
