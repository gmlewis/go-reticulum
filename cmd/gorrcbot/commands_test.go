// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// commandFixture builds a connected session and a registry over it.
func commandFixture(t *testing.T, cfg *BotConfig) (*registry, *hubSession, *fakeHub) {
	t.Helper()
	if cfg == nil {
		cfg = defaultTestConfig()
	}
	session, fake := newReplySession(t, cfg)
	b := session.bot
	reg := newRegistry(b)
	return reg, session, fake
}

// runLines runs one command line and returns the reply lines.
func runLines(t *testing.T, reg *registry, session *hubSession, line string) []string {
	t.Helper()
	return reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot "+line, peerHashFor(0x11)),
		Room:    "general",
		Command: line,
		Nick:    "gorrcbot",
		Now:     time.Now(),
	})
}

// TestRegistryNamesAreStable asserts the command set: the official hub bot's
// names minus the '!' plus the mesh additions, with no duplicates.
func TestRegistryNamesAreStable(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	var names []string
	for _, cmd := range reg.commands {
		names = append(names, cmd.name)
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{
		"botinfo", "dn", "dnotice", "dnoticecap", "dnoticeme", "help", "ping",
		"uptime", "weather", "whoami", "wx", "seen", "members", "rooms", "id",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the command registry has no %q command (have %v)", want, names)
		}
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Errorf("command %q is registered twice", name)
		}
		seen[name] = true
		if strings.HasPrefix(name, "!") {
			t.Errorf("command %q carries a '!' prefix, which gorrcbot never uses", name)
		}
	}
	if len(reg.aliases) == 0 {
		t.Error("the registry has no aliases; dn/dnotice and weather/wx share a handler")
	}
	_ = session
}

// TestHelpIsGeneratedFromTheRegistry asserts help is never hand-maintained: the
// listing matches the registry and the wording mirrors the official bot's.
func TestHelpIsGeneratedFromTheRegistry(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "help")
	if len(lines) != 1 {
		t.Fatalf("help returned %v lines, want 1: %v", len(lines), lines)
	}
	got := lines[0]
	if !strings.HasPrefix(got, "Commands: ") {
		t.Fatalf("help = %q, want it to start like the official bot's listing", got)
	}
	list := strings.TrimPrefix(got, "Commands: ")
	names := strings.Split(list, ", ")
	if len(names) != len(reg.commands) {
		t.Errorf("help lists %v commands, the registry has %v", len(names), len(reg.commands))
	}
	for i, cmd := range reg.commands {
		if i < len(names) && names[i] != cmd.name {
			t.Errorf("help entry %v = %q, want %q", i, names[i], cmd.name)
		}
	}
	// The official bot's listing is sorted; matching it makes the bot feel
	// familiar to a client that already knows Beleth.
	sorted := append([]string(nil), names...)
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1] > sorted[i] {
			t.Errorf("help listing %q is not sorted", list)
			break
		}
	}
}

// TestHelpForOneCommand asserts help <command> explains that command, and an
// unknown name gets one short line.
func TestHelpForOneCommand(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)

	lines := runLines(t, reg, session, "help dn")
	if len(lines) != 1 {
		t.Fatalf("help dn returned %v lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "dnotice") || !strings.Contains(lines[0], "Usage") {
		t.Errorf("help dn = %q, want the command's usage", lines[0])
	}

	lines = runLines(t, reg, session, "help bogus")
	if len(lines) != 1 {
		t.Fatalf("help bogus returned %v lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "unknown command") {
		t.Errorf("help bogus = %q, want the unknown-command line", lines[0])
	}
}

// TestUnknownCommandIsOneShortLine asserts an unrecognised command produces
// exactly one short line naming the way to get help.
func TestUnknownCommandIsOneShortLine(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "bogus")
	if len(lines) != 1 {
		t.Fatalf("an unknown command returned %v lines, want exactly 1", len(lines))
	}
	if len(lines[0]) > 120 {
		t.Errorf("the unknown-command line is %v characters, want it short", len(lines[0]))
	}
	for _, want := range []string{"unknown command", "help"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("unknown-command line = %q, want it to mention %q", lines[0], want)
		}
	}
}

// TestBareAddressShowsHelp asserts addressing the bot with no command is not an
// error: it answers with the command listing.
func TestBareAddressShowsHelp(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "Commands: ") {
		t.Errorf("a bare address returned %q, want the command listing", lines)
	}
}

// TestPingMirrorsTheOfficialWording asserts ping answers the official bot's way.
func TestPingMirrorsTheOfficialWording(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "ping")
	if len(lines) != 1 || lines[0] != "pong" {
		t.Errorf("ping = %q, want %q as the official bot answers", lines, "pong")
	}
}

// TestWhoamiMirrorsTheOfficialWording asserts whoami reports the caller's nick
// and full identity hash, exactly as the official bot does.
func TestWhoamiMirrorsTheOfficialWording(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	requester := peerHashFor(0x11)
	fake.setKnownPeer(hexString(requester), "Alice")
	lines := reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot whoami", requester),
		Room:    "general",
		Command: "whoami",
		Nick:    "gorrcbot",
		Now:     time.Now(),
	})
	if len(lines) != 1 {
		t.Fatalf("whoami returned %v lines, want 1", len(lines))
	}
	if !strings.HasPrefix(lines[0], "You are Alice ("+hexString(requester)+")") {
		t.Errorf("whoami = %q, want the official \"You are <nick> (<hash>)\" shape", lines[0])
	}
}

// TestWhoamiWithUnknownRequesterFallsBackToTheHash asserts an unidentified
// caller still gets a useful answer.
func TestWhoamiWithUnknownRequesterFallsBackToTheHash(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	requester := peerHashFor(0x12)
	lines := reg.Run(&commandRequest{
		Session: session,
		Msg:     &rrc.RRCMessage{Kind: "msg", Room: "general", Src: requester, Text: "@gorrcbot whoami"},
		Room:    "general",
		Command: "whoami",
		Now:     time.Now(),
	})
	if len(lines) != 1 {
		t.Fatalf("whoami returned %v lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], hexString(requester)) {
		t.Errorf("whoami = %q, want it to include the caller's hash", lines[0])
	}
}

// TestBotinfoMirrorsTheOfficialWording asserts botinfo reports the nick, the
// destination name, the hub hash, and the room and command counts in the
// official bot's shape.
func TestBotinfoMirrorsTheOfficialWording(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	fake.serverName = "rnscommunity"
	fake.hubVersion = "0.3.2"
	lines := runLines(t, reg, session, "botinfo")
	if len(lines) != 1 {
		t.Fatalf("botinfo returned %v lines, want 1", len(lines))
	}
	got := lines[0]
	for _, want := range []string{
		"nickname=", "dest=rrc.hub", "hub=" + fakeHubOne, "rooms=", "commands=",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("botinfo = %q, want it to contain %q", got, want)
		}
	}
	if !strings.Contains(got, "hubname=rnscommunity") {
		t.Errorf("botinfo = %q, want it to name the hub server", got)
	}
	if !strings.Contains(got, "hubversion=0.3.2") {
		t.Errorf("botinfo = %q, want it to report the hub version", got)
	}
	if !strings.Contains(got, "identity="+fakeHubTwo) {
		t.Errorf("botinfo = %q, want it to report the bot's identity hash", got)
	}
}

// TestUptimeMirrorsTheOfficialWording asserts uptime reports a runtime and the
// connection age in the official shape.
func TestUptimeMirrorsTheOfficialWording(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "uptime")
	if len(lines) != 1 {
		t.Fatalf("uptime returned %v lines, want 1", len(lines))
	}
	got := lines[0]
	if !strings.HasPrefix(got, "Uptime: runtime=") {
		t.Errorf("uptime = %q, want the official \"Uptime: runtime=...\" shape", got)
	}
	if !strings.Contains(got, "hub="+fakeHubOne) {
		t.Errorf("uptime = %q, want it to name the hub", got)
	}
	// The official shape is HH:MM:SS for every duration.
	for field := range strings.SplitSeq(strings.TrimPrefix(got, "Uptime: "), "; ") {
		_, value, ok := strings.Cut(field, "=")
		if !ok {
			t.Errorf("uptime = %q, want every field to be name=value", got)
			continue
		}
		value = strings.TrimSuffix(value, ".")
		if strings.Contains(field, "runtime") || strings.Contains(field, "connection") {
			if len(value) != 8 || value[2] != ':' || value[5] != ':' {
				t.Errorf("uptime field %q = %q, want HH:MM:SS", field, value)
			}
		}
	}
}

// TestDnoticeCapMirrorsTheOfficialWording asserts dnoticecap reports the hub's
// advertised capability in the official shape.
func TestDnoticeCapMirrorsTheOfficialWording(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)

	lines := runLines(t, reg, session, "dnoticecap")
	if len(lines) != 1 || lines[0] != "Direct NOTICE supported: False" {
		t.Errorf("dnoticecap = %q, want %q without the capability", lines,
			"Direct NOTICE supported: False")
	}

	fake.setCapability(rrc.CapDirectNotice, true)
	lines = runLines(t, reg, session, "dnoticecap")
	if len(lines) != 1 || lines[0] != "Direct NOTICE supported: True" {
		t.Errorf("dnoticecap = %q, want %q with the capability", lines,
			"Direct NOTICE supported: True")
	}
}

// TestDnoticeCapReportsATarget asserts dnoticecap <target> resolves the target
// and reports whether it is reachable, which is what makes it useful.
func TestDnoticeCapReportsATarget(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	fake.setCapability(rrc.CapDirectNotice, true)
	peer := peerHashFor(0x13)
	fake.setKnownPeer(hexString(peer), "Bob")

	lines := runLines(t, reg, session, "dnoticecap Bob")
	if len(lines) != 1 {
		t.Fatalf("dnoticecap Bob returned %v lines, want 1", len(lines))
	}
	for _, want := range []string{"Bob", hexString(peer)[:12], "True"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("dnoticecap Bob = %q, want it to mention %q", lines[0], want)
		}
	}

	lines = runLines(t, reg, session, "dnoticecap nobody")
	if len(lines) != 1 || !strings.Contains(lines[0], "no such") {
		t.Errorf("dnoticecap nobody = %q, want one line saying the target is unknown", lines)
	}
}

// TestDnoticeUsageMatchesTheOfficialWording asserts the usage line is the
// official bot's, with gorrcbot's missing '!' and its wider target syntax.
func TestDnoticeUsageMatchesTheOfficialWording(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range []string{"dn", "dnotice"} {
		lines := runLines(t, reg, session, line)
		if len(lines) != 1 {
			t.Fatalf("%v returned %v lines, want 1", line, len(lines))
		}
		if !strings.Contains(lines[0], "Usage: dnotice <nick|hash|me> <text>") {
			t.Errorf("%v = %q, want the dnotice usage line", line, lines[0])
		}
	}
}

// TestDnoticeSendsADirectNotice asserts dn/dnotice resolve a target and deliver
// the text as one direct NOTICE, confirming with the official one-line reply.
func TestDnoticeSendsADirectNotice(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	fake.setCapability(rrc.CapDirectNotice, true)
	peer := peerHashFor(0x14)
	fake.setKnownPeer(hexString(peer), "Bob")

	lines := runLines(t, reg, session, "dnotice Bob hello there")
	if len(lines) != 1 {
		t.Fatalf("dnotice returned %v lines, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Direct NOTICE sent to Bob") {
		t.Errorf("dnotice = %q, want the official confirmation shape", lines[0])
	}
	sent := fake.directList()
	if len(sent) != 1 || sent[0] != "hello there" {
		t.Fatalf("direct notices = %q, want the text delivered once", sent)
	}

	// The hash form works too, and 'me' addresses the caller.
	lines = runLines(t, reg, session, "dn "+hexString(peer)+" by hash")
	if len(lines) != 1 || !strings.Contains(lines[0], "Direct NOTICE sent to") {
		t.Fatalf("dn by hash = %q, want a confirmation", lines)
	}
	if got := fake.directList(); len(got) != 2 || got[1] != "by hash" {
		t.Errorf("direct notices = %q, want the second delivery", got)
	}
}

// TestDnoticeMeAddressesTheCaller asserts 'me' resolves to the requester.
func TestDnoticeMeAddressesTheCaller(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	fake.setCapability(rrc.CapDirectNotice, true)
	requester := peerHashFor(0x15)
	fake.setKnownPeer(hexString(requester), "Alice")

	lines := reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot dnotice me hello self", requester),
		Room:    "general",
		Command: "dnotice me hello self",
		Now:     time.Now(),
	})
	if len(lines) != 1 {
		t.Fatalf("dnotice me returned %v lines, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Direct NOTICE sent to") {
		t.Errorf("dnotice me = %q, want a confirmation", lines[0])
	}
	if got := fake.directList(); len(got) != 1 || got[0] != "hello self" {
		t.Errorf("direct notices = %q, want the text delivered to the caller", got)
	}
}

// TestDnoticeRejectsBadInput asserts every failure explains itself in one line
// instead of failing silently.
func TestDnoticeRejectsBadInput(t *testing.T) {
	t.Parallel()

	t.Run("no arguments", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := commandFixture(t, nil)
		lines := runLines(t, reg, session, "dnotice")
		if len(lines) != 1 || !strings.Contains(lines[0], "Usage:") {
			t.Errorf("dnotice = %q, want the usage line", lines)
		}
	})

	t.Run("target without text", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := commandFixture(t, nil)
		lines := runLines(t, reg, session, "dnotice Bob")
		if len(lines) != 1 || !strings.Contains(lines[0], "Usage:") {
			t.Errorf("dnotice Bob = %q, want the usage line", lines)
		}
	})

	t.Run("unknown target", func(t *testing.T) {
		t.Parallel()
		reg, session, _ := commandFixture(t, nil)
		lines := runLines(t, reg, session, "dnotice nobody hi")
		if len(lines) != 1 || !strings.Contains(lines[0], "no such") {
			t.Errorf("dnotice nobody hi = %q, want one line saying the target is unknown", lines)
		}
	})

	t.Run("hub without the capability", func(t *testing.T) {
		t.Parallel()
		reg, session, fake := commandFixture(t, nil)
		peer := peerHashFor(0x16)
		fake.setKnownPeer(hexString(peer), "Bob")
		lines := runLines(t, reg, session, "dnotice Bob hi")
		if len(lines) != 1 || !strings.Contains(lines[0], "not supported") {
			t.Errorf("dnotice without the capability = %q, want one line saying so", lines)
		}
	})

	t.Run("oversized text", func(t *testing.T) {
		t.Parallel()
		reg, session, fake := commandFixture(t, nil)
		fake.setCapability(rrc.CapDirectNotice, true)
		peer := peerHashFor(0x17)
		fake.setKnownPeer(hexString(peer), "Bob")
		lines := runLines(t, reg, session, "dnotice Bob "+strings.Repeat("x", rns.MDU*2))
		if len(lines) != 1 || !strings.Contains(lines[0], "too long") {
			t.Errorf("oversized dnotice = %q, want one line saying the text is too long", lines)
		}
		if len(fake.directList()) != 0 {
			t.Errorf("direct notices = %q, want none for oversized text", fake.directList())
		}
	})
}

// TestDnoticemeMirrorsTheOfficialWording asserts dnoticeme is a self-test of the
// direct-notice path, with the official bot's one-line confirmation.
func TestDnoticemeMirrorsTheOfficialWording(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	fake.setCapability(rrc.CapDirectNotice, true)

	lines := runLines(t, reg, session, "dnoticeme")
	if len(lines) != 1 || lines[0] != "Usage: dnoticeme <text>" {
		t.Errorf("dnoticeme = %q, want %q", lines, "Usage: dnoticeme <text>")
	}

	requester := peerHashFor(0x18)
	fake.setKnownPeer(hexString(requester), "Alice")
	lines = reg.Run(&commandRequest{
		Session: session,
		Msg:     addressedMessageFrom("general", "@gorrcbot dnoticeme hello me", requester),
		Room:    "general",
		Command: "dnoticeme hello me",
		Now:     time.Now(),
	})
	if len(lines) != 1 {
		t.Fatalf("dnoticeme returned %v lines, want 1", len(lines))
	}
	if lines[0] != "Direct NOTICE sent to self" {
		t.Errorf("dnoticeme = %q, want %q", lines[0], "Direct NOTICE sent to self")
	}
	if got := fake.directList(); len(got) != 1 || got[0] != "hello me" {
		t.Errorf("direct notices = %q, want the text delivered to the caller", got)
	}
}

// TestDnoticemeWithoutTheCapabilityExplainsItself asserts a self-test that cannot
// run says so instead of pretending to succeed.
func TestDnoticemeWithoutTheCapabilityExplainsItself(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "dnoticeme hi")
	if len(lines) != 1 || !strings.Contains(lines[0], "not supported") {
		t.Errorf("dnoticeme without the capability = %q, want one line saying so", lines)
	}
}

// TestWeatherIsPendingWithoutAProvider asserts weather/wx are honest: with no
// configured provider they say so in one line rather than inventing a forecast.
func TestWeatherIsPendingWithoutAProvider(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range []string{"weather", "wx", "weather London", "wx London"} {
		lines := runLines(t, reg, session, line)
		if len(lines) != 1 {
			t.Fatalf("%v returned %v lines, want 1", line, len(lines))
		}
		if !strings.Contains(lines[0], "not configured") {
			t.Errorf("%v = %q, want one line saying no provider is configured", line, lines[0])
		}
	}
}

// TestSeenReportsTheLastMessageFromAPeer asserts the seen command answers from
// the hub's own buffer, which is why it exists on a mesh: a client that was
// offline can catch up.
func TestSeenReportsTheLastMessageFromAPeer(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	peer := peerHashFor(0x19)
	fake.setKnownPeer(hexString(peer), "Carol")
	fake.messages["general"] = []*rrc.RRCMessage{
		{Kind: "msg", Room: "general", Src: peer, Nick: "Carol", Text: "hello there", Ts: 1700000000000},
	}

	lines := runLines(t, reg, session, "seen Carol")
	if len(lines) != 1 {
		t.Fatalf("seen Carol returned %v lines, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Carol") || !strings.Contains(lines[0], "hello there") {
		t.Errorf("seen Carol = %q, want the peer and the message", lines[0])
	}

	lines = runLines(t, reg, session, "seen nobody")
	if len(lines) != 1 || !strings.Contains(lines[0], "no such") {
		t.Errorf("seen nobody = %q, want one line saying the peer is unknown", lines)
	}

	lines = runLines(t, reg, session, "seen")
	if len(lines) != 1 || !strings.Contains(lines[0], "Usage:") {
		t.Errorf("seen = %q, want the usage line", lines)
	}
}

// TestMembersReportsTheRoomMemberList asserts members answers from the hub's
// member list in the official /who notice shape.
func TestMembersReportsTheRoomMemberList(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	fake.setKnownPeer(hexString(peerHashFor(0x1a)), "Dave")
	lines := runLines(t, reg, session, "members")
	if len(lines) != 1 {
		t.Fatalf("members returned %v lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "members in general") || !strings.Contains(lines[0], "Dave") {
		t.Errorf("members = %q, want the official /who notice shape", lines[0])
	}
}

// TestRoomsReportsTheJoinedRooms asserts rooms lists what the bot is in, which
// is what makes it reachable.
func TestRoomsReportsTheJoinedRooms(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "rooms")
	if len(lines) != 1 || !strings.Contains(lines[0], "general") {
		t.Errorf("rooms = %q, want one line naming the joined rooms", lines)
	}
}

// TestIDReportsTheBotsIdentity asserts id reports the identity hash and the
// trigger nicks, so an operator can paste it into a client.
func TestIDReportsTheBotsIdentity(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	lines := runLines(t, reg, session, "id")
	if len(lines) != 1 {
		t.Fatalf("id returned %v lines, want 1", len(lines))
	}
	for _, want := range []string{fakeHubTwo, "gorrcbot", "rrc.hub"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("id = %q, want it to contain %q", lines[0], want)
		}
	}
}

// TestCommandsNeverExceedOneEnvelopePerLine asserts every reply the registry can
// produce fits the wire, which is the contract the whole reply path rests on.
func TestCommandsNeverExceedOneEnvelopePerLine(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	fake.serverName = "rnscommunity"
	fake.hubVersion = "0.3.2"
	fake.setCapability(rrc.CapDirectNotice, true)
	for i := range 40 {
		fake.setKnownPeer(hexString(peerHashFor(byte(i))), "peer"+string(rune('a'+i%26)))
	}

	for _, cmd := range reg.commands {
		for _, line := range []string{cmd.name, cmd.name + " x"} {
			lines := runLines(t, reg, session, line)
			if len(lines) > 4 {
				t.Errorf("%v replied with %v lines, want at most 4", line, len(lines))
			}
			for i, reply := range lines {
				chunks, err := splitNoticeText(mustHex(fakeHubTwo), "general", "gorrcbot", reply, 12)
				if err != nil {
					t.Fatalf("%v line %v: %v", line, i, err)
				}
				for _, chunk := range chunks {
					size, err := noticeEnvelopeSize(mustHex(fakeHubTwo), "general", "gorrcbot", chunk)
					if err != nil {
						t.Fatalf("%v line %v: %v", line, i, err)
					}
					if size > rns.MDU {
						t.Errorf("%v replied with a %v-byte envelope, larger than the MDU %v",
							line, size, rns.MDU)
					}
				}
			}
		}
	}
}

// TestCommandsAreCaseInsensitive asserts a client that types HELP is understood.
func TestCommandsAreCaseInsensitive(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)
	for _, line := range []string{"HELP", "Help", "PING", "WhoAmI"} {
		lines := runLines(t, reg, session, line)
		if len(lines) == 0 {
			t.Errorf("%v returned nothing, want a reply", line)
		}
		if strings.Contains(lines[0], "unknown command") {
			t.Errorf("%v = %q, want the command to be recognised", line, lines[0])
		}
	}
}

// TestRegistryExposesSummariesAndUsage asserts every command documents itself,
// which is what makes the generated help useful.
func TestRegistryExposesSummariesAndUsage(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, nil)
	for _, cmd := range reg.commands {
		if strings.TrimSpace(cmd.summary) == "" {
			t.Errorf("command %q has no summary", cmd.name)
		}
		if cmd.run == nil {
			t.Errorf("command %q has no handler", cmd.name)
		}
		if cmd.name != strings.ToLower(cmd.name) {
			t.Errorf("command %q is not lowercase", cmd.name)
		}
	}
}
