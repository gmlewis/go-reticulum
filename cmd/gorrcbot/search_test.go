// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

// searchRow builds one row for a search test in a named room, at a given offset
// before the base clock.
func searchRow(room, nick, text string, ago time.Duration, src []byte) *rrc.RRCMessage {
	at := catchupBase.Add(-ago)
	return &rrc.RRCMessage{
		Kind: "msg",
		Room: room,
		Src:  src,
		Nick: nick,
		Text: text,
		Ts:   at.UnixMilli(),
		ID:   fmt.Sprintf("%v-%v-%v", room, nick, at.UnixMilli()),
	}
}

// searchFixture builds a registry whose hub holds the given live rows and whose
// storage holds a real history file, exactly as a running client writes one. The
// hub is configured and joined in every room named by joinedRooms.
func searchFixture(t *testing.T, joinedRooms []string, live, persisted map[string][]*rrc.RRCMessage) (*registry, *hubSession, *fakeHub) {
	t.Helper()
	cfg := defaultTestConfig()
	cfg.StorageDir = tempDir(t)
	if len(persisted) > 0 {
		writeHistoryDir(t, cfg.StorageDir, fakeHubOne, persisted)
	}
	reg, session, fake := commandFixture(t, cfg)
	setHubRooms(session, fake, joinedRooms...)
	maps.Copy(fake.messages, live)
	return reg, session, fake
}

// runSearchLine runs one command line as if it arrived at the base clock, in
// #general, so match ordering and clocks are deterministic.
func runSearchLine(t *testing.T, reg *registry, session *hubSession, line string) []string {
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

// TestSearchFindsMatchesAcrossRooms asserts a search reports every match, newest
// first, naming the room each one came from, and that the header counts rooms as
// well as matches.
func TestSearchFindsMatchesAcrossRooms(t *testing.T) {
	t.Parallel()

	live := map[string][]*rrc.RRCMessage{
		"general": {
			searchRow("general", "Alice", "the retibooks link is old", 3*time.Hour, peerHashFor(0x22)),
			searchRow("general", "Alice", "nothing to see", 2*time.Hour, peerHashFor(0x22)),
			searchRow("general", "Bob", "RetiBooks is at retibooks.example", 30*time.Minute, peerHashFor(0x33)),
		},
		"lounge": {
			searchRow("lounge", "Carol", "also retibooks", 90*time.Minute, peerHashFor(0x66)),
		},
	}
	reg, session, _ := searchFixture(t, []string{"general", "lounge"}, live, nil)

	assertLines(t, runSearchLine(t, reg, session, "search retibooks"), []string{
		"3 matches for retibooks in 2 rooms (newest first)",
		"[22:30] #general Bob: RetiBooks is at retibooks.example",
		"[21:30] #lounge Carol: also retibooks",
		"[20:00] #general Alice: the retibooks link is old",
	})

	// One match reads as one match in one room.
	single := map[string][]*rrc.RRCMessage{
		"general": {searchRow("general", "Bob", "RetiBooks", 10*time.Minute, peerHashFor(0x33))},
	}
	one, oneSession, _ := searchFixture(t, []string{"general"}, single, nil)
	assertLines(t, runSearchLine(t, one, oneSession, "search retibooks"), []string{
		"1 match for retibooks in 1 room (newest first)",
		"[22:50] #general Bob: RetiBooks",
	})
}

// TestSearchIsCaseInsensitiveAndPhraseAware asserts matching ignores case and
// treats a multi-word argument as one phrase, so a term that spans a space is
// found only where the words really are adjacent.
func TestSearchIsCaseInsensitiveAndPhraseAware(t *testing.T) {
	t.Parallel()

	live := map[string][]*rrc.RRCMessage{
		"general": {
			searchRow("general", "Alice", "What Is Up with the node?", 20*time.Minute, peerHashFor(0x22)),
			searchRow("general", "Bob", "what a mess, up to you", 10*time.Minute, peerHashFor(0x33)),
		},
	}
	reg, session, _ := searchFixture(t, []string{"general"}, live, nil)

	assertLines(t, runSearchLine(t, reg, session, "search WHAT IS UP"), []string{
		"1 match for WHAT IS UP in 1 room (newest first)",
		"[22:40] #general Alice: What Is Up with the node?",
	})
}

// TestSearchReadsPersistedHistory asserts a match the bot only ever wrote to disk
// is found, and that a row held both live and on disk is reported once.
func TestSearchReadsPersistedHistory(t *testing.T) {
	t.Parallel()

	persisted := map[string][]*rrc.RRCMessage{
		"general": {
			searchRow("general", "Alice", "old retibooks note", 40*time.Minute, peerHashFor(0x22)),
			searchRow("general", "Bob", "retibooks again", 30*time.Minute, peerHashFor(0x33)),
		},
	}
	live := map[string][]*rrc.RRCMessage{
		"general": {searchRow("general", "Bob", "retibooks again", 30*time.Minute, peerHashFor(0x33))},
	}
	reg, session, _ := searchFixture(t, []string{"general"}, live, persisted)

	assertLines(t, runSearchLine(t, reg, session, "search retibooks"), []string{
		"2 matches for retibooks in 1 room (newest first)",
		"[22:30] #general Bob: retibooks again",
		"[22:20] #general Alice: old retibooks note",
	})
}

// TestSearchScopesToOneRoom asserts an explicit room narrows the search, with or
// without the '#', in any case, and that an unknown room answers honestly.
func TestSearchScopesToOneRoom(t *testing.T) {
	t.Parallel()

	live := map[string][]*rrc.RRCMessage{
		"general": {searchRow("general", "Alice", "retibooks here", 20*time.Minute, peerHashFor(0x22))},
		"lounge":  {searchRow("lounge", "Carol", "retibooks there", 10*time.Minute, peerHashFor(0x66))},
	}
	reg, session, _ := searchFixture(t, []string{"general", "lounge"}, live, nil)

	for _, line := range []string{"search retibooks #lounge", "search retibooks LOUNGE"} {
		assertLines(t, runSearchLine(t, reg, session, line), []string{
			"1 match for retibooks in 1 room (newest first)",
			"[22:50] #lounge Carol: retibooks there",
		})
	}

	// A bare token that is not a joined room is part of the phrase, so the
	// honest unknown-room answer needs the explicit "#room" form.
	assertLines(t, runSearchLine(t, reg, session, "search retibooks nosuchroom"), []string{
		"no messages match retibooks nosuchroom in the rooms I have joined",
	})
	assertLines(t, runSearchLine(t, reg, session, "search retibooks #nosuchroom"), []string{
		`no room named "nosuchroom": I have joined general, lounge`,
	})
}

// TestSearchTreatsASingleTokenAsATerm asserts a room's own name stays searchable:
// one token is always a term, never a room filter, so "search general" searches
// for the word rather than answering with a usage line.
func TestSearchTreatsASingleTokenAsATerm(t *testing.T) {
	t.Parallel()

	live := map[string][]*rrc.RRCMessage{
		"general": {searchRow("general", "Alice", "the general room is quiet", 20*time.Minute, peerHashFor(0x22))},
	}
	reg, session, _ := searchFixture(t, []string{"general"}, live, nil)

	assertLines(t, runSearchLine(t, reg, session, "search general"), []string{
		"1 match for general in 1 room (newest first)",
		"[22:40] #general Alice: the general room is quiet",
	})
	// With a second token, the last one is a room filter again.
	assertLines(t, runSearchLine(t, reg, session, "search general general"), []string{
		"1 match for general in 1 room (newest first)",
		"[22:40] #general Alice: the general room is quiet",
	})
}

// TestSearchReportsNoMatches asserts a miss is answered honestly, scoped to what
// was actually searched.
func TestSearchReportsNoMatches(t *testing.T) {
	t.Parallel()

	live := map[string][]*rrc.RRCMessage{
		"general": {searchRow("general", "Alice", "hello", 20*time.Minute, peerHashFor(0x22))},
		"lounge":  {searchRow("lounge", "Carol", "hi", 10*time.Minute, peerHashFor(0x66))},
	}
	reg, session, _ := searchFixture(t, []string{"general", "lounge"}, live, nil)

	assertLines(t, runSearchLine(t, reg, session, "search zzzz"), []string{
		"no messages match zzzz in the rooms I have joined",
	})
	assertLines(t, runSearchLine(t, reg, session, "search zzzz lounge"), []string{
		"no messages match zzzz in #lounge",
	})
}

// TestSearchRejectsUnusableTerms asserts every unusable argument gets the usage
// line, never an echo of the rejected text and never a match.
func TestSearchRejectsUnusableTerms(t *testing.T) {
	t.Parallel()

	reg, session, _ := searchFixture(t, []string{"general"}, nil, nil)

	for _, line := range []string{
		"search",
		"search    ",
		"search ...",
		"search " + strings.Repeat("x", maxSearchTermBytes+1),
		"search \x1b[2J",
		"search \x07\x07",
	} {
		lines := runSearchLine(t, reg, session, line)
		assertLines(t, lines, []string{"Usage: " + searchUsage})
		for _, reply := range lines {
			if strings.ContainsAny(reply, "\x1b\x07") {
				t.Errorf("%q was echoed into %q", line, reply)
			}
		}
	}
}

// TestSearchStripsEscapesFromRowsAndTerm asserts peer-supplied text cannot carry
// a terminal escape into a search reply, including when the escape is inside the
// term the asker typed: the readable part still matches and is still reported.
func TestSearchStripsEscapesFromRowsAndTerm(t *testing.T) {
	t.Parallel()

	live := map[string][]*rrc.RRCMessage{
		"general": {
			searchRow("general", "Ev\x1b[31mil", "the \x1b]0;pwned\x07retibooks link", 20*time.Minute, peerHashFor(0x22)),
		},
	}
	reg, session, _ := searchFixture(t, []string{"general"}, live, nil)

	assertLines(t, runSearchLine(t, reg, session, "search retibooks"), []string{
		"1 match for retibooks in 1 room (newest first)",
		"[22:40] #general Evil: the retibooks link",
	})

	// The term itself is sanitized before it is matched and echoed.
	assertLines(t, runSearchLine(t, reg, session, "search reti\x1b[31mbooks"), []string{
		"1 match for retibooks in 1 room (newest first)",
		"[22:40] #general Evil: the retibooks link",
	})
}

// TestSearchSkipsTheBotsOwnLinesAndTheCommandItself asserts a search never
// reports the bot's own replies (which are noise, and include the reply being
// built) nor the command being answered.
func TestSearchSkipsTheBotsOwnLinesAndTheCommandItself(t *testing.T) {
	t.Parallel()

	command := searchRow("general", "Alice", "@gorrcbot search retibooks", 5*time.Second, peerHashFor(0x11))
	command.ID = "the-current-request"
	live := map[string][]*rrc.RRCMessage{
		"general": {
			command,
			searchRow("general", "gorrcbot", "Commands: help, search", 12*time.Minute, mustHex(replyOwnHash)),
			searchRow("general", "Alice", "retibooks is up", 20*time.Minute, peerHashFor(0x22)),
		},
	}
	reg, session, _ := searchFixture(t, []string{"general"}, live, nil)

	assertLines(t, reg.Run(&commandRequest{
		Session: session,
		Msg:     command,
		Room:    "general",
		Command: "search retibooks",
		Nick:    "gorrcbot",
		Now:     catchupBase,
	}), []string{
		"1 match for retibooks in 1 room (newest first)",
		"[22:40] #general Alice: retibooks is up",
	})
}

// TestSearchStaysWithinTheReplyBudget asserts the reply spends its budget on the
// header, the newest matches that fit, and an honest trailer.
func TestSearchStaysWithinTheReplyBudget(t *testing.T) {
	t.Parallel()

	var rows []*rrc.RRCMessage
	for i := range 5 {
		rows = append(rows, searchRow("general", "Alice", fmt.Sprintf("match %v", i), time.Duration(10+i)*time.Minute, peerHashFor(0x22)))
	}
	live := map[string][]*rrc.RRCMessage{"general": rows}

	cfg := defaultTestConfig()
	cfg.MaxReplyLines = 4
	cfg.StorageDir = tempDir(t)
	reg, session, fake := commandFixture(t, cfg)
	setHubRooms(session, fake, "general")
	maps.Copy(fake.messages, live)

	assertLines(t, runSearchLine(t, reg, session, "search match"), []string{
		"5 matches for match in 1 room (newest first)",
		"[22:50] #general Alice: match 0",
		"[22:49] #general Alice: match 1",
		"… 3 more matches not shown",
	})
}

// TestSearchIsRegistered asserts the command is in the table, so help lists it and
// the dispatcher can reach it.
func TestSearchIsRegistered(t *testing.T) {
	t.Parallel()

	reg, session, _ := searchFixture(t, []string{"general"}, nil, nil)
	cmd, ok := reg.byName["search"]
	if !ok {
		t.Fatalf("search is not registered; names = %v", reg.names())
	}
	if cmd.usage != searchUsage {
		t.Errorf("usage = %q, want %q", cmd.usage, searchUsage)
	}
	assertLines(t, reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot help search", peerHashFor(0x11)),
		Room:    "general",
		Command: "help search",
		Nick:    "gorrcbot",
		Now:     catchupBase,
	}), append([]string{"search — " + cmd.summary + ". Usage: " + searchUsage}, cmd.detail...))
}
