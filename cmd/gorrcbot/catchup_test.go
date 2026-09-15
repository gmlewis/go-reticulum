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

// catchupBase is the fixed wall clock the digest tests hang off: 2026-09-14
// 23:00 UTC, with rows placed relative to it.
var catchupBase = time.Date(2026, 9, 14, 23, 0, 0, 0, time.UTC)

// roomRow builds one room row for a digest test, at a given offset before the
// base clock.
func roomRow(kind, nick, text string, ago time.Duration, src []byte) *rrc.RRCMessage {
	at := catchupBase.Add(-ago)
	return &rrc.RRCMessage{
		Kind: kind,
		Room: "general",
		Src:  src,
		Nick: nick,
		Text: text,
		Ts:   at.UnixMilli(),
		ID:   fmt.Sprintf("%v-%v", kind, at.UnixMilli()),
	}
}

// catchupFixture builds a registry whose hub holds the given live rows and, when
// persisted is not nil, whose storage directory holds a real history file
// written exactly as the RRC client writes one.
func catchupFixture(t *testing.T, cfg *BotConfig, live map[string][]*rrc.RRCMessage, persisted map[string][]*rrc.RRCMessage) (*registry, *hubSession, *fakeHub) {
	t.Helper()
	if cfg == nil {
		cfg = defaultTestConfig()
	}
	cfg.StorageDir = tempDir(t)
	if len(persisted) > 0 {
		writeHistoryDir(t, cfg.StorageDir, fakeHubOne, persisted)
	}
	reg, session, fake := commandFixture(t, cfg)
	maps.Copy(fake.messages, live)
	return reg, session, fake
}

// setHubRooms points the session's hub at the given configured rooms and marks
// them joined, which is the state the engine leaves behind after a join. The
// reply fixture always starts with #general alone.
func setHubRooms(session *hubSession, fake *fakeHub, rooms ...string) {
	configs := make([]RoomConfig, 0, len(rooms))
	for _, room := range rooms {
		configs = append(configs, RoomConfig{Name: room})
		fake.rooms[room] = true
		session.joinedOK[room] = true
	}
	session.cfg.Rooms = configs
}

// runCatchupLine runs one command line as if it arrived at the base clock, so the
// digest's window and clocks are deterministic.
func runCatchupLine(t *testing.T, reg *registry, session *hubSession, line string) []string {
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

// assertLines compares reply lines exactly, so a wording change fails loudly.
func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("lines = %q (%v lines), want %q (%v lines)", got, len(got), want, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %v = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestCatchupDigestsOnlyConversation asserts the digest keeps speech, actions and
// attributed replies, and drops hub chatter, the standing greeting, undated rows
// and the bot's own lines.
func TestCatchupDigestsOnlyConversation(t *testing.T) {
	t.Parallel()

	reg, session, _ := catchupFixture(t, nil, nil, nil)
	ownHash := mustHex(replyOwnHash)
	botRow := roomRow("msg", "gorrcbot", "Commands: help, ping", 100*time.Minute, ownHash)
	live := []*rrc.RRCMessage{
		roomRow("msg", "Alice", "hello", 110*time.Minute, peerHashFor(0x22)),
		roomRow("action", "Bob", "waves", 109*time.Minute, peerHashFor(0x33)),
		roomRow("notice", "gorrbot", "an answer", 108*time.Minute, peerHashFor(0x44)),
		roomRow("notice", "", "*** Alice joined", 107*time.Minute, nil),
		roomRow("system", "", "hub restarting", 106*time.Minute, nil),
		roomRow("error", "", "boom", 105*time.Minute, nil),
		roomRow("msg", "Alice", "undated", 0, peerHashFor(0x22)),
		botRow,
	}
	live[6].Ts = 0
	greeting := roomRow("notice", "gorrbot", "Welcome to the hub", 104*time.Minute, peerHashFor(0x44))
	greeting.Pinned = true
	live = append(live, greeting)

	session.conn.(*fakeHub).messages["general"] = live

	assertLines(t, runCatchupLine(t, reg, session, "catchup"), []string{
		"catchup in general: 3 messages from 3 speakers since 21:00 (2h ago)",
		"[21:12] gorrbot: an answer",
		"[21:11] Bob: waves",
		"[21:10] Alice: hello",
	})
}

// TestCatchupAnchorsOnTheAskersLastRemark asserts a bare catchup starts where the
// asker left off, not at the default window, and that the catchup they just typed
// is neither the anchor nor a digest row.
func TestCatchupAnchorsOnTheAskersLastRemark(t *testing.T) {
	t.Parallel()

	command := roomRow("msg", "Alice", "@gorrcbot catchup", 10*time.Second, peerHashFor(0x11))
	command.ID = "the-current-request"
	live := []*rrc.RRCMessage{
		command,
		roomRow("msg", "Alice", "see you later", 30*time.Minute, peerHashFor(0x11)),
		roomRow("msg", "Bob", "before you left", 45*time.Minute, peerHashFor(0x33)),
		roomRow("msg", "Bob", "while you were away", 20*time.Minute, peerHashFor(0x33)),
	}
	reg, session, _ := catchupFixture(t, nil, map[string][]*rrc.RRCMessage{"general": live}, nil)

	request := &commandRequest{
		Session: session,
		Msg:     command,
		Room:    "general",
		Command: "catchup",
		Nick:    "gorrcbot",
		Now:     catchupBase,
	}
	assertLines(t, reg.Run(request), []string{
		"catchup in general: 2 messages from 2 speakers since 22:30 (30m ago)",
		"[22:40] Bob: while you were away",
		"[22:30] Alice: see you later",
	})
}

// TestCatchupAnchorsOnTheLastRemarkNotTheLastCommand asserts a repeated catchup
// does not anchor on the previous catchup command.
func TestCatchupAnchorsOnTheLastRemarkNotTheLastCommand(t *testing.T) {
	t.Parallel()

	live := []*rrc.RRCMessage{
		roomRow("msg", "Alice", "@gorrcbot catchup", 20*time.Minute, peerHashFor(0x11)),
		roomRow("msg", "Alice", "good morning", 90*time.Minute, peerHashFor(0x11)),
		roomRow("msg", "Bob", "morning", 80*time.Minute, peerHashFor(0x33)),
	}
	reg, session, _ := catchupFixture(t, nil, map[string][]*rrc.RRCMessage{"general": live}, nil)

	// The anchor is the asker's last real remark (90m ago), not the catchup
	// command they typed 20m ago, and that command is not a digest row either.
	assertLines(t, runCatchupLine(t, reg, session, "catchup"), []string{
		"catchup in general: 2 messages from 2 speakers since 21:30 (1h ago)",
		"[21:40] Bob: morning",
		"[21:30] Alice: good morning",
	})
}

// TestCatchupHonoursAnExplicitWindow asserts the window argument, including a
// window long enough to reach rows older than a day, which render as dates.
func TestCatchupHonoursAnExplicitWindow(t *testing.T) {
	t.Parallel()

	live := []*rrc.RRCMessage{
		roomRow("msg", "Alice", "recent", 10*time.Minute, peerHashFor(0x22)),
		roomRow("msg", "Bob", "yesterday", 25*time.Hour, peerHashFor(0x33)),
		roomRow("msg", "Bob", "last week", 6*24*time.Hour, peerHashFor(0x33)),
	}
	reg, session, _ := catchupFixture(t, nil, map[string][]*rrc.RRCMessage{"general": live}, nil)

	assertLines(t, runCatchupLine(t, reg, session, "catchup 30m"), []string{
		"catchup in general: 1 message from 1 speaker since 22:30 (30m ago)",
		"[22:50] Alice: recent",
	})
	assertLines(t, runCatchupLine(t, reg, session, "catchup 2d"), []string{
		"catchup in general: 2 messages from 2 speakers since 2026-09-12 23:00 (2d ago)",
		"[22:50] Alice: recent",
		"[2026-09-13 22:00] Bob: yesterday",
	})
	// The maximum window reaches everything, and the oldest row is dated.
	assertLines(t, runCatchupLine(t, reg, session, "catchup 7d"), []string{
		"catchup in general: 3 messages from 2 speakers since 2026-09-07 23:00 (7d ago)",
		"[22:50] Alice: recent",
		"[2026-09-13 22:00] Bob: yesterday",
		"[2026-09-08 23:00] Bob: last week",
	})
}

// TestCatchupAcceptsTheRoomInAnyForm asserts an explicit room, with or without
// the '#', in any case, and that a named room overrides the room the request
// arrived in.
func TestCatchupAcceptsTheRoomInAnyForm(t *testing.T) {
	t.Parallel()

	live := map[string][]*rrc.RRCMessage{
		"general": {roomRow("msg", "Alice", "in general", 10*time.Minute, peerHashFor(0x22))},
		"lounge":  {roomRow("msg", "Bob", "in the lounge", 5*time.Minute, peerHashFor(0x33))},
	}
	reg, session, fake := catchupFixture(t, nil, live, nil)
	setHubRooms(session, fake, "general", "lounge")

	for _, line := range []string{"catchup #lounge", "catchup LOUNGE"} {
		assertLines(t, runCatchupLine(t, reg, session, line), []string{
			"catchup in lounge: 1 message from 1 speaker since 21:00 (2h ago)",
			"[22:55] Bob: in the lounge",
		})
	}
}

// TestCatchupTreatsAShortRoomNameAsARoom asserts a room named like a window stays
// reachable by name: a duration-shaped token that names a joined room is a room.
func TestCatchupTreatsAShortRoomNameAsARoom(t *testing.T) {
	t.Parallel()

	live := map[string][]*rrc.RRCMessage{
		"2h": {roomRow("msg", "Bob", "in the odd room", 5*time.Minute, peerHashFor(0x33))},
	}
	reg, session, fake := catchupFixture(t, nil, live, nil)
	setHubRooms(session, fake, "general", "2h")

	assertLines(t, runCatchupLine(t, reg, session, "catchup 2h"), []string{
		"catchup in 2h: 1 message from 1 speaker since 21:00 (2h ago)",
		"[22:55] Bob: in the odd room",
	})
}

// TestCatchupRejectsUnknownRoomsAndBadArguments asserts every unusable argument
// gets the usage line or the room listing, never a silent guess.
func TestCatchupRejectsUnknownRoomsAndBadArguments(t *testing.T) {
	t.Parallel()

	reg, session, _ := catchupFixture(t, nil, nil, nil)

	assertLines(t, runCatchupLine(t, reg, session, "catchup nosuchroom"), []string{
		`no room named "nosuchroom": I have joined general`,
	})
	assertLines(t, runCatchupLine(t, reg, session, "catchup general lounge"), []string{
		"Usage: catchup [room] [window]",
	})
	assertLines(t, runCatchupLine(t, reg, session, "catchup 1h 2h"), []string{
		"Usage: catchup [room] [window]",
	})
	assertLines(t, runCatchupLine(t, reg, session, "catchup #"), []string{
		"Usage: catchup [room] [window]",
	})
}

// TestCatchupAnswersTheQuietRoom asserts a room with nothing to report says so
// once, rather than sending an empty digest.
func TestCatchupAnswersTheQuietRoom(t *testing.T) {
	t.Parallel()

	live := []*rrc.RRCMessage{roomRow("msg", "Alice", "hours ago", 5*time.Hour, peerHashFor(0x22))}
	reg, session, _ := catchupFixture(t, nil, map[string][]*rrc.RRCMessage{"general": live}, nil)

	assertLines(t, runCatchupLine(t, reg, session, "catchup"), []string{
		"general has been quiet since 21:00",
	})

	// A room the bot has joined but never seen a message in is quiet too.
	empty, emptySession, _ := catchupFixture(t, nil, nil, nil)
	assertLines(t, runCatchupLine(t, empty, emptySession, "catchup"), []string{
		"general has been quiet since 21:00",
	})
}

// TestCatchupFallsBackToTheFirstConfiguredRoom asserts a direct request — which
// carries no room — digests the hub's first configured room.
func TestCatchupFallsBackToTheFirstConfiguredRoom(t *testing.T) {
	t.Parallel()

	live := map[string][]*rrc.RRCMessage{
		"lobby": {roomRow("msg", "Alice", "in the lobby", 10*time.Minute, peerHashFor(0x22))},
	}
	reg, session, fake := catchupFixture(t, nil, live, nil)
	setHubRooms(session, fake, "lobby", "general")

	lines := reg.Run(&commandRequest{
		Session: session,
		Msg:     directMessageFrom("catchup", peerHashFor(0x11)),
		Command: "catchup",
		Nick:    "gorrcbot",
		Now:     catchupBase,
	})
	assertLines(t, lines, []string{
		"catchup in lobby: 1 message from 1 speaker since 21:00 (2h ago)",
		"[22:50] Alice: in the lobby",
	})
}

// TestCatchupMergesPersistedHistory asserts the digest covers rows the bot wrote
// to disk before it restarted, and that a row held both live and on disk is
// counted once.
func TestCatchupMergesPersistedHistory(t *testing.T) {
	t.Parallel()

	persisted := []*rrc.RRCMessage{
		roomRow("msg", "Bob", "before the restart", 40*time.Minute, peerHashFor(0x33)),
		roomRow("msg", "Alice", "also on disk", 30*time.Minute, peerHashFor(0x22)),
	}
	live := []*rrc.RRCMessage{
		roomRow("msg", "Alice", "also on disk", 30*time.Minute, peerHashFor(0x22)),
		roomRow("msg", "Carol", "after the restart", 10*time.Minute, peerHashFor(0x66)),
	}
	reg, session, _ := catchupFixture(t, nil, map[string][]*rrc.RRCMessage{"general": live},
		map[string][]*rrc.RRCMessage{"general": persisted})

	assertLines(t, runCatchupLine(t, reg, session, "catchup 2h"), []string{
		"catchup in general: 3 messages from 3 speakers since 21:00 (2h ago)",
		"[22:50] Carol: after the restart",
		"[22:30] Alice: also on disk",
		"[22:20] Bob: before the restart",
	})
}

// TestCatchupStaysWithinTheReplyBudget asserts the digest spends the reply budget
// on a header, as many rows as fit, and an honest trailer.
func TestCatchupStaysWithinTheReplyBudget(t *testing.T) {
	t.Parallel()

	var live []*rrc.RRCMessage
	for i := range 5 {
		live = append(live, roomRow("msg", "Alice", fmt.Sprintf("x%v", i), time.Duration(10+i)*time.Minute, peerHashFor(0x22)))
	}

	cfg := defaultTestConfig()
	cfg.MaxReplyLines = 4
	reg, session, _ := catchupFixture(t, cfg, map[string][]*rrc.RRCMessage{"general": live}, nil)

	assertLines(t, runCatchupLine(t, reg, session, "catchup"), []string{
		"catchup in general: 5 messages from 1 speaker since 21:00 (2h ago)",
		"[22:50] Alice: x0",
		"[22:49] Alice: x1",
		"… 3 earlier messages not shown",
	})

	// A budget with no room for a body line still answers with the summary.
	tight := defaultTestConfig()
	tight.MaxReplyLines = 1
	tightReg, tightSession, _ := catchupFixture(t, tight, map[string][]*rrc.RRCMessage{"general": live}, nil)
	assertLines(t, runCatchupLine(t, tightReg, tightSession, "catchup"), []string{
		"catchup in general: 5 messages from 1 speaker since 21:00 (2h ago)",
	})
}

// TestCatchupNeverEchoesTerminalEscapes asserts peer-controlled text is stripped
// before it is repeated under the bot's name: a room row, a nick and a row that
// is nothing but escapes must not reach a client's terminal.
func TestCatchupNeverEchoesTerminalEscapes(t *testing.T) {
	t.Parallel()

	live := []*rrc.RRCMessage{
		roomRow("msg", "Al\x1b[31mice", "safe\x1b]0;pwned\x07text", 20*time.Minute, peerHashFor(0x22)),
		roomRow("msg", "Bob", "\x1b[2J\x1b[H", 15*time.Minute, peerHashFor(0x33)),
		roomRow("msg", "Carol", strings.Repeat("x", 200), 10*time.Minute, peerHashFor(0x66)),
	}
	reg, session, _ := catchupFixture(t, nil, map[string][]*rrc.RRCMessage{"general": live}, nil)

	lines := runCatchupLine(t, reg, session, "catchup")
	for _, line := range lines {
		if strings.ContainsAny(line, "\x1b\x07") {
			t.Errorf("line %q carries a terminal escape", line)
		}
	}
	assertLines(t, lines, []string{
		"catchup in general: 2 messages from 2 speakers since 21:00 (2h ago)",
		"[22:50] Carol: " + strings.Repeat("x", maxCatchupTextBytes) + "…",
		"[22:40] Alice: safetext",
	})
}

// TestParseWindow asserts which arguments are windows, that they are clamped, and
// that anything else is not one.
func TestParseWindow(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		token string
		want  time.Duration
		ok    bool
	}{
		{token: "15m", want: 15 * time.Minute, ok: true},
		{token: "2h", want: 2 * time.Hour, ok: true},
		{token: "1d", want: 24 * time.Hour, ok: true},
		{token: "90s", want: 90 * time.Second, ok: true},
		{token: "07m", want: 7 * time.Minute, ok: true},
		{token: "999d", want: catchupMaxWindow, ok: true},
		{token: "0m"},
		{token: "m"},
		{token: "2x"},
		{token: "-1h"},
		{token: "h2"},
		{token: ""},
		{token: "1 h"},
		{token: "general"},
	} {
		got, ok := parseWindow(tt.token)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parseWindow(%q) = %v, %v, want %v, %v", tt.token, got, ok, tt.want, tt.ok)
		}
	}
}

// TestFormatClockSwitchesAtADay asserts a digest line is unambiguous: a clock for
// the last day, a date and clock for anything older, and a clock for a timestamp
// in the future rather than a negative age.
func TestFormatClockSwitchesAtADay(t *testing.T) {
	t.Parallel()

	now := catchupBase
	for _, tt := range []struct {
		name string
		at   time.Time
		want string
	}{
		{name: "just now", at: now, want: "23:00"},
		{name: "an hour ago", at: now.Add(-time.Hour), want: "22:00"},
		{name: "almost a day ago", at: now.Add(-23 * time.Hour), want: "00:00"},
		{name: "a day ago", at: now.Add(-24 * time.Hour), want: "2026-09-13 23:00"},
		{name: "in the future", at: now.Add(time.Hour), want: "00:00"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := formatClock(tt.at, now); got != tt.want {
				t.Errorf("formatClock(%v) = %q, want %q", tt.at, got, tt.want)
			}
		})
	}
}

// TestSafeEcho asserts the hygiene the digest relies on, including that text
// which sanitizes away entirely becomes empty, which callers skip.
func TestSafeEcho(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		text  string
		limit int
		want  string
	}{
		{name: "plain text is unchanged", text: "hello there", limit: 40, want: "hello there"},
		{name: "surrounding spaces trimmed", text: "  hello  ", limit: 40, want: "hello"},
		{name: "escape stripped", text: "a\x1b[31mb", limit: 40, want: "ab"},
		{name: "osc stripped", text: "a\x1b]0;title\x07b", limit: 40, want: "ab"},
		{name: "newline becomes a space", text: "a\nb", limit: 40, want: "a b"},
		{name: "truncated with a marker", text: strings.Repeat("x", 20), limit: 5, want: "xxxxx…"},
		{name: "only escapes", text: "\x1b[2J", limit: 40, want: ""},
		{name: "empty", text: "", limit: 40, want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := safeEcho(tt.text, tt.limit); got != tt.want {
				t.Errorf("safeEcho(%q, %v) = %q, want %q", tt.text, tt.limit, got, tt.want)
			}
		})
	}
}

// TestCatchupIsRegistered asserts the command is in the table, so help lists it
// and the dispatcher can reach it.
func TestCatchupIsRegistered(t *testing.T) {
	t.Parallel()

	reg, session, _ := catchupFixture(t, nil, nil, nil)
	cmd, ok := reg.byName["catchup"]
	if !ok {
		t.Fatalf("catchup is not registered; names = %v", reg.names())
	}
	if cmd.usage != catchupUsage {
		t.Errorf("usage = %q, want %q", cmd.usage, catchupUsage)
	}
	assertLines(t, reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot help catchup", peerHashFor(0x11)),
		Room:    "general",
		Command: "help catchup",
		Nick:    "gorrcbot",
		Now:     catchupBase,
	}), append([]string{"catchup — " + cmd.summary + ". Usage: " + catchupUsage}, cmd.detail...))
}

// TestDigestable asserts the digest's row policy directly, including the shapes a
// hub can send that must never reach a digest.
func TestDigestable(t *testing.T) {
	t.Parallel()

	own := hexString(peerHashFor(0x55))
	rows := []struct {
		name string
		row  *rrc.RRCMessage
		want bool
	}{
		{name: "speech", row: roomRow("msg", "Alice", "hi", time.Minute, peerHashFor(0x22)), want: true},
		{name: "action", row: roomRow("action", "Alice", "waves", time.Minute, peerHashFor(0x22)), want: true},
		{name: "attributed notice", row: roomRow("notice", "gorrbot", "answer", time.Minute, peerHashFor(0x44)), want: true},
		{name: "hub chatter", row: roomRow("notice", "", "joined", time.Minute, nil)},
		{name: "system", row: roomRow("system", "", "restart", time.Minute, nil)},
		{name: "error", row: roomRow("error", "", "boom", time.Minute, nil)},
		{name: "our own line", row: roomRow("msg", "gorrcbot", "Commands:", time.Minute, peerHashFor(0x55))},
		{name: "nil row"},
	}
	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := digestable(tt.row, own); got != tt.want {
				t.Errorf("digestable(%v) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}

	undated := roomRow("msg", "Alice", "no clock", time.Minute, peerHashFor(0x22))
	undated.Ts = 0
	if digestable(undated, own) {
		t.Error("digestable(an undated row) = true, want false")
	}
	greeting := roomRow("notice", "gorrbot", "welcome", time.Minute, peerHashFor(0x44))
	greeting.Pinned = true
	if digestable(greeting, own) {
		t.Error("digestable(the standing greeting) = true, want false")
	}
}

// TestAddressedToNick asserts which rows count as commands rather than remarks.
func TestAddressedToNick(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		text string
		want bool
	}{
		{text: "@gorrcbot catchup", want: true},
		{text: "  @Gorrcbot ping", want: true},
		{text: "gorrcbot catchup"},
		{text: "hey @gorrcbot"},
		{text: ""},
	} {
		if got := addressedToNick(tt.text, "gorrcbot"); got != tt.want {
			t.Errorf("addressedToNick(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
	if addressedToNick("@anything", "") {
		t.Error("addressedToNick with no nick = true, want false")
	}
}
