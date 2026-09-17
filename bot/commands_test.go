// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package bot

import (
	"slices"
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

// TestRegistryNamesAreStable asserts the exact command set: the official hub
// bot's names minus the '!' plus this bot's own additions, with no duplicates and
// no name carrying a prefix. It is exact rather than a substring check because
// "id" is a substring of "dnotice", which would hide a missing command.
func TestRegistryNamesAreStable(t *testing.T) {
	t.Parallel()

	reg, _, _ := commandFixture(t, nil)
	want := []string{
		// The official RNS Community Hub bot's set, minus its '!' prefix.
		"botinfo", "dn", "dnotice", "dnoticecap", "dnoticeme", "help", "ping",
		"uptime", "weather", "whoami", "wx",
		// This bot's own additions.
		"catchup", "flight", "id", "kjv", "launches", "lxmf", "members", "msg", "path", "rooms",
		"search", "seen", "unwatch", "watch", "watches",
		// The field assistant's navigation commands.
		"dist", "loc", "proj", "sun", "whereami",
		// The field assistant's emergency and situation commands.
		"checkin", "firstaid", "med", "rx", "sitrep", "sos", "triage",
		// The field assistant's propagation and mesh commands.
		"net", "solar", "spacewx",
		// The field assistant's tactical references.
		"conv", "morse", "signal",
		// The field assistant's aviation weather commands.
		"metar", "wxalert",
		// The field assistant's lunar and marine commands.
		"buoy", "coldwater", "immersion", "moon", "river", "tide",
		// The field assistant's offline cell and repeater finder, and its
		// aliases.
		"cell", "mast", "repeater", "tower",
		// The shared discovery pagination shortcuts.
		"more", "next",
	}
	// The registry sorts its rows, so the expectation is sorted too; the groups
	// above are for the reader, not the comparison.
	slices.Sort(want)
	var names []string
	for _, cmd := range reg.commands {
		names = append(names, cmd.name)
		if strings.HasPrefix(cmd.name, "!") {
			t.Errorf("command %q carries a '!' prefix, which gorrcbot never uses", cmd.name)
		}
	}
	if got := strings.Join(names, " "); got != strings.Join(want, " ") {
		t.Errorf("command set = %v,\nwant %v\nmissing: %v\nunexpected: %v",
			names, want, missingFrom(want, names), missingFrom(names, want))
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			t.Errorf("command %q is registered twice", name)
		}
		seen[name] = true
	}
	if len(reg.aliases) == 0 {
		t.Error("the registry has no aliases; dn/dnotice and weather/wx share a handler")
	}
}

// missingFrom reports the entries of want that are absent from have, in want's
// order, so a failure names exactly what is wrong.
func missingFrom(want, have []string) []string {
	present := make(map[string]bool, len(have))
	for _, name := range have {
		present[name] = true
	}
	var missing []string
	for _, name := range want {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	return missing
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

// TestHelpForOneCommand asserts help <command> explains that command: the first
// line is its purpose and usage, and the lines after it are its guidance. An
// unknown name still gets one short line.
func TestHelpForOneCommand(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, nil)

	lines := runLines(t, reg, session, "help dn")
	if len(lines) == 0 {
		t.Fatalf("help dn returned nothing")
	}
	if !strings.Contains(lines[0], "dnotice") || !strings.Contains(lines[0], "Usage") {
		t.Errorf("help dn = %q, want the command's usage", lines[0])
	}
	if want := 1 + len(reg.byName["dn"].detail); len(lines) != want {
		t.Errorf("help dn returned %v lines, want %v", len(lines), want)
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
// official bot's shape, plus the bot's own version; the hub's version is named
// only when it differs from the bot's.
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
		t.Errorf("botinfo = %q, want it to report the hub version when it differs", got)
	}
	if !strings.Contains(got, "botversion="+rns.VERSION) {
		t.Errorf("botinfo = %q, want it to report the bot's own version %v", got, rns.VERSION)
	}
	if !strings.Contains(got, "identity="+fakeHubTwo) {
		t.Errorf("botinfo = %q, want it to report the bot's identity hash", got)
	}
	// A hub built from the same tree advertises the bot's own version, so the
	// field is omitted rather than repeating it.
	fake.hubVersion = rns.VERSION
	if got := runLines(t, reg, session, "botinfo")[0]; strings.Contains(got, "hubversion=") {
		t.Errorf("botinfo = %q, want no hubversion when the hub reports the bot's version", got)
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

// TestPeerTokenAcceptsTheAtSigil asserts every command that takes a peer token
// accepts the "@" a room member types to address someone: "@Bob" and "Bob" name
// the same peer, so both must resolve identically.
func TestPeerTokenAcceptsTheAtSigil(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	fake.setCapability(rrc.CapDirectNotice, true)
	peer := peerHashFor(0x13)
	fake.setKnownPeer(hexString(peer), "Bob")
	fake.messages["general"] = []*rrc.RRCMessage{
		{Kind: "msg", Room: "general", Src: peer, Nick: "Bob", Text: "hello there", Ts: 1700000000000},
	}

	for _, line := range []string{"dnoticecap", "seen"} {
		bare := runLines(t, reg, session, line+" Bob")
		sigil := runLines(t, reg, session, line+" @Bob")
		if !slices.Equal(bare, sigil) {
			t.Errorf("%v @Bob = %q, want the same as %v Bob = %q", line, sigil, line, bare)
		}
		if len(sigil) == 0 || strings.Contains(sigil[0], "no such") {
			t.Errorf("%v @Bob = %q, want the target resolved", line, sigil)
		}
	}
}

// TestNormalizePeerToken asserts a peer token is trimmed and one leading sigil is
// dropped, so "@glenn", " glenn " and "glenn" name the same peer while a token
// that is only the sigil normalizes to empty.
func TestNormalizePeerToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		want  string
	}{
		{name: "bare nick", token: "glenn", want: "glenn"},
		{name: "sigil nick", token: "@glenn", want: "glenn"},
		{name: "padded nick", token: "  glenn  ", want: "glenn"},
		{name: "padded sigil nick", token: "  @glenn  ", want: "glenn"},
		{name: "sigil only", token: "@", want: ""},
		{name: "double sigil drops one", token: "@@glenn", want: "@glenn"},
		{name: "empty", token: "", want: ""},
		{name: "whitespace", token: "   ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizePeerToken(tt.token); got != tt.want {
				t.Errorf("normalizePeerToken(%q) = %q, want %q", tt.token, got, tt.want)
			}
		})
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
	if !strings.Contains(lines[0], "members of general") || !strings.Contains(lines[0], "Dave") {
		t.Errorf("members = %q, want the official /who notice shape", lines[0])
	}
}

// TestEchoedPeerTextIsStrippedOfEscapes asserts the commands that repeat
// peer-supplied text — a room member's nick, a member's message — never publish
// raw terminal escapes under the bot's name. A NOTICE is rendered by everybody
// else's terminal, and a nick is chosen by its owner.
func TestEchoedPeerTextIsStrippedOfEscapes(t *testing.T) {
	t.Parallel()

	const hostileNick = "Ev\x1b[31mil"
	reg, session, fake := commandFixture(t, nil)
	peer := peerHashFor(0x1b)
	fake.setKnownPeer(hexString(peer), hostileNick)
	fake.messages["general"] = []*rrc.RRCMessage{{
		Kind: "msg",
		Room: "general",
		Src:  peer,
		Nick: hostileNick,
		Text: "look\x1b]0;pwned\x07here",
		Ts:   1700000000000,
	}}

	for _, line := range []string{"members", "seen " + hostileNick, "seen " + hexString(peer)} {
		for _, reply := range runLines(t, reg, session, line) {
			if strings.ContainsAny(reply, "\x1b\x07") {
				t.Errorf("%v = %q, which carries a terminal escape", line, reply)
			}
		}
	}

	// The readable part of the nick and the message still reaches the answer.
	lines := runLines(t, reg, session, "seen "+hexString(peer))
	if len(lines) != 1 || !strings.Contains(lines[0], "Evil") || !strings.Contains(lines[0], "look") {
		t.Errorf("seen = %q, want the readable name and message", lines)
	}
	members := runLines(t, reg, session, "members")
	if len(members) != 1 || !strings.Contains(members[0], "Evil") {
		t.Errorf("members = %q, want the readable nick", members)
	}

	// A nick or message that is nothing but escapes leaves nothing to show.
	fake.setKnownPeer(hexString(peerHashFor(0x1c)), "\x1b[2J")
	fake.messages["general"] = append(fake.messages["general"], &rrc.RRCMessage{
		Kind: "msg", Room: "general", Src: peerHashFor(0x1c), Nick: "\x1b[2J", Text: "\x1b[H", Ts: 1700000001000,
	})
	if got := runLines(t, reg, session, "seen "+hexString(peerHashFor(0x1c))); !strings.Contains(got[0], "no readable text") {
		t.Errorf("seen on an all-escape message = %q, want the honest placeholder", got)
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
// which is what makes the generated help useful: a summary, a handler, a detail
// the asker can act on, and an explanation that fits one reply.
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
		if len(cmd.detail) == 0 {
			t.Errorf("command %q has no detail; help %v would explain nothing", cmd.name, cmd.name)
		}
		if got := 1 + len(cmd.detail) + len(cmd.configHint); got > DefaultMaxReplyLines {
			t.Errorf("help %v would produce %v lines, more than the default max_reply_lines %v",
				cmd.name, got, DefaultMaxReplyLines)
		}
	}
}

// TestMembersReplyIsNotProtocolTraffic asserts the members reply cannot be
// mistaken for hub protocol traffic. "members in <room>: ..." is exactly the
// shape of the hub's own /who reply (rrc/commands.go, handleWho), and a client
// treats a NOTICE of that shape as a member-set update: it parses it
// (rrc.ParseWhoNotice), overwrites the room's member list, and silently consumes
// it whenever its periodic auto-/who is outstanding — so the reply reached
// nobody's screen while clobbering what every client believed about the room.
func TestMembersReplyIsNotProtocolTraffic(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake.setKnownPeer(hexString(peer.Hash), "Dave")

	tests := []struct {
		name string
		line string
	}{
		{name: "a room with members", line: "members"},
		{name: "a room with none reported", line: "members empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := runLines(t, reg, session, tt.line)
			if len(lines) != 1 {
				t.Fatalf("lines = %v, want one", lines)
			}
			if room, _, isWho := rrc.ParseWhoNotice(lines[0]); isWho {
				t.Errorf("the reply %q parses as a /who reply for room %q", lines[0], room)
			}
			if rooms := rrc.ParseRoomListNotice(lines[0]); rooms != nil {
				t.Errorf("the reply %q parses as a room list: %v", lines[0], rooms)
			}
			if strings.HasPrefix(lines[0], "room ") {
				t.Errorf("the reply %q starts with the reserved protocol ack prefix", lines[0])
			}
		})
	}
}

// TestMembersWorksOnTheDirectRoute asserts a direct NOTICE — which carries no room
// — is answered for the room the bot joined rather than with a usage line. A
// private "/msg gobot members" was answered "Usage: members [room]" live, which
// asks the asker to name a room they never chose and the bot is usually alone in.
func TestMembersWorksOnTheDirectRoute(t *testing.T) {
	t.Parallel()

	reg, session, fake := commandFixture(t, nil)
	peer, err := rns.NewIdentity(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake.setKnownPeer(hexString(peer.Hash), "Dave")

	lines := reg.Run(&commandRequest{
		Session: session,
		Msg:     directMessageFrom("members", peerHashFor(0x11)),
		Command: "members",
		Nick:    "gobot",
		Now:     time.Now(),
	})
	if len(lines) == 0 {
		t.Fatal("no answer at all")
	}
	if strings.HasPrefix(lines[0], "Usage:") {
		t.Fatalf("answer = %q, want the member list for the joined room", lines[0])
	}
	if !strings.Contains(lines[0], "members of general:") {
		t.Errorf("answer = %q, want it to name the room it answered for", lines[0])
	}
}

// TestUnknownCommandNamesTheConfiguredNick asserts the line that tells an asker
// how to address the bot names a nick the bot actually answers to. A direct NOTICE
// carries no addressed nick, so the fallback used to be the built-in default:
// live, "/msg gobot nosuchcommand" answered "try @gorrcbot help" under a bot whose
// nick is gobot, sending the asker to a name nobody owns.
func TestUnknownCommandNamesTheConfiguredNick(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      func(*BotConfig)
		session  func(*hubSession)
		nick     string
		room     string
		wantNick string
	}{
		{
			name:     "a direct notice uses the configured nick",
			cfg:      func(c *BotConfig) { c.Nick = "gobot" },
			wantNick: "gobot",
		},
		{
			// The session carries its own hub entry, so the override belongs
			// there: it is the config the trigger policy is actually asked about.
			name: "a room override wins for its room",
			cfg:  func(c *BotConfig) { c.Nick = "gobot" },
			session: func(s *hubSession) {
				s.cfg.RespondTo = map[string]string{"general": "roombot"}
			},
			room:     "general",
			wantNick: "roombot",
		},
		{
			name:     "an addressed nick is echoed as it was written",
			cfg:      func(c *BotConfig) { c.Nick = "gobot" },
			nick:     "gobot",
			room:     "general",
			wantNick: "gobot",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultTestConfig()
			tt.cfg(cfg)
			reg, session, _ := commandFixture(t, cfg)
			if tt.session != nil {
				tt.session(session)
			}
			lines := reg.Run(&commandRequest{
				Session: session,
				Msg:     directMessageFrom("nosuchcommand", peerHashFor(0x11)),
				Room:    tt.room,
				Command: "nosuchcommand",
				Nick:    tt.nick,
				Now:     time.Now(),
			})
			assertLines(t, lines, []string{"unknown command — try @" + tt.wantNick + " help"})
		})
	}
}

// gnssFixture builds a registry and session whose GNSS source holds fix. A zero
// fix leaves the registry with no receiver, which is the headless case every
// zero-argument fallback must survive.
func gnssFixture(t *testing.T, fix GPSFix) (*registry, *hubSession) {
	t.Helper()
	reg, session, _ := commandFixture(t, nil)
	if fix.Valid {
		reader := NewGPSReader(nil)
		reader.SetFix(fix)
		t.Cleanup(func() { _ = reader.Close() })
		reg.gps = reader
	}
	return reg, session
}

// TestZeroArgumentCommandsUseTheGNSSFix asserts a fix makes "tower near" and
// "tide near" answer with the operator's own three closest sites, which is the
// question a person with cold hands actually asks: one word, no coordinates
// typed from a screen.
func TestZeroArgumentCommandsUseTheGNSSFix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line string
		want []string
	}{
		{"tower near", []string{"Tower sites near " + refPlus10, "km", "MHz"}},
		{"tide near", []string{"Tide stations near " + refPlus10}},
		{"cell near", []string{"Tower sites near " + refPlus10}},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()
			reg, session := gnssFixture(t, sfFix())
			lines := runLines(t, reg, session, tc.line)
			joined := strings.Join(lines, "\n")
			if strings.Contains(joined, "Usage:") {
				t.Fatalf("%v asked for an argument despite a live fix:\n%v", tc.line, joined)
			}
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Errorf("%v = %v, want it to contain %q", tc.line, lines, want)
				}
			}
		})
	}
}

// TestZeroArgumentCommandsWithoutAFixStillAsk asserts the fallback never
// invents a position: with no fix the command asks for the argument it needs.
func TestZeroArgumentCommandsWithoutAFixStillAsk(t *testing.T) {
	t.Parallel()

	cases := []struct {
		line string
		want string
	}{
		{"tower near", "Usage: tower near <place|coords|pluscode>"},
		{"tide near", "Usage: tide near <place|coords|pluscode>"},
		{"sun", "Usage: " + sunUsage},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()
			reg, session := gnssFixture(t, GPSFix{})
			lines := runLines(t, reg, session, tc.line)
			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("%v without a fix = %v, want %q", tc.line, lines, tc.want)
			}
		})
	}
}

// TestSunUsesTheGNSSFix asserts a bare "sun" reports the almanac for the
// operator's own position, and that a date may still be given on its own.
func TestSunUsesTheGNSSFix(t *testing.T) {
	t.Parallel()

	reg, session := gnssFixture(t, sfFix())
	joined := strings.Join(runLines(t, reg, session, "sun"), "\n")
	if strings.Contains(joined, "Usage:") {
		t.Fatalf("a bare sun asked for a location despite a live fix:\n%v", joined)
	}
	if !strings.Contains(joined, refPlus10) {
		t.Errorf("sun = %q, want the fix's Plus Code %v", joined, refPlus10)
	}
	if !strings.Contains(joined, "Almanac for") {
		t.Errorf("sun = %q, want an almanac", joined)
	}

	joined = strings.Join(runLines(t, reg, session, "sun 2026-06-21"), "\n")
	if strings.Contains(joined, "Usage:") {
		t.Fatalf("a dated sun asked for a location despite a live fix:\n%v", joined)
	}
	if !strings.Contains(joined, "2026-06-21") {
		t.Errorf("sun 2026-06-21 = %q, want the requested date", joined)
	}
}

// TestSOSRaisesAtTheGNSSFix asserts a beacon raised with no typed location uses
// the operator's verified position, and that the receiver facts travel with it.
func TestSOSRaisesAtTheGNSSFix(t *testing.T) {
	t.Parallel()

	reg, session := gnssFixture(t, sfFix())
	lines := runLines(t, reg, session, "sos RED two hikers, one leg fracture")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, refPlus10) {
		t.Errorf("sos = %v, want the fix's Plus Code %v", lines, refPlus10)
	}
	if !strings.Contains(joined, "RED") {
		t.Errorf("sos = %v, want the triage level", lines)
	}
	if !strings.Contains(joined, "two hikers") {
		t.Errorf("sos = %v, want the details", lines)
	}
	if !strings.Contains(joined, "GNSS") {
		t.Errorf("sos = %v, want the receiver facts attached", lines)
	}
}

// TestSOSDefaultsTriageAndDetailsAtTheGNSSFix asserts the shortest possible
// distress call works: "sos" alone raises a RED beacon at the current position,
// because somebody typing one word is somebody in trouble.
func TestSOSDefaultsTriageAndDetailsAtTheGNSSFix(t *testing.T) {
	t.Parallel()

	reg, session := gnssFixture(t, sfFix())
	lines := runLines(t, reg, session, "sos")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, refPlus10) || !strings.Contains(joined, "RED") {
		t.Errorf("a bare sos = %v, want a RED beacon at %v", lines, refPlus10)
	}
}

// TestSOSWithoutAFixStillRequiresALocation asserts the automatic context never
// turns a mistyped request into a beacon somewhere arbitrary.
func TestSOSWithoutAFixStillRequiresALocation(t *testing.T) {
	t.Parallel()

	reg, session := gnssFixture(t, GPSFix{})
	lines := runLines(t, reg, session, "sos RED two hikers")
	if !strings.Contains(strings.Join(lines, "\n"), "Usage: "+sosUsage) {
		t.Errorf("sos without a fix = %v, want the usage line", lines)
	}
}

// TestSOSExplicitLocationStillWins asserts a typed location is never replaced
// by the receiver's: a call for a party somewhere else must stay about them.
func TestSOSExplicitLocationStillWins(t *testing.T) {
	t.Parallel()

	reg, session := gnssFixture(t, sfFix())
	lines := runLines(t, reg, session, "sos 39.7392,-104.9903 RED climber hurt")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "85FQP2Q5+MV") {
		t.Errorf("sos with a typed location = %v, want the typed position", lines)
	}
	if strings.Contains(joined, refPlus10) {
		t.Errorf("sos with a typed location = %v, want the live fix ignored", lines)
	}
}

// TestWhereAmIUsesTheGNSSFixThroughTheRegistry asserts the registry's own fix
// accessor is what every zero-argument command reads, so one receiver feeds
// them all.
func TestWhereAmIUsesTheGNSSFixThroughTheRegistry(t *testing.T) {
	t.Parallel()

	reg, session := gnssFixture(t, sfFix())
	fix, ok := reg.currentFix()
	if !ok {
		t.Fatal("currentFix reported no fix for a valid one")
	}
	if fix.Lat != refLat || fix.Lng != refLng {
		t.Errorf("currentFix = %v,%v, want %v,%v", fix.Lat, fix.Lng, refLat, refLng)
	}
	joined := strings.Join(runLines(t, reg, session, "whereami"), "\n")
	if !strings.Contains(joined, refPlus10) {
		t.Errorf("whereami = %q, want the fix's Plus Code", joined)
	}

	empty, _ := gnssFixture(t, GPSFix{})
	if _, ok := empty.currentFix(); ok {
		t.Error("currentFix reported a fix with no receiver")
	}
}
