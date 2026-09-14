// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// replyOwnHash is the identity hash the reply tests run as. It is deliberately
// different from the fake hub's address, so a reply that mixes the two up fails.
const replyOwnHash = fakeHubTwo

// newReplySession builds a hub session whose fake hub is connected and joined to
// the given rooms, which is the state a reply is allowed in.
func newReplySession(t *testing.T, cfg *BotConfig, rooms ...string) (*hubSession, *fakeHub) {
	t.Helper()
	if len(rooms) == 0 {
		rooms = []string{"general"}
	}
	hubCfg := &HubConfig{Name: "One", Destination: fakeHubOne, Rooms: []RoomConfig{{Name: "general"}}}
	fake := newFakeHub(hubCfg)
	for _, room := range rooms {
		fake.rooms[room] = true
	}
	fake.setStatus(rrc.StatusConnected)
	b := newBot(cfg, BotPaths{}, nil, mustHex(replyOwnHash), nil, botHooks{})
	b.startedAt = time.Now()
	s := newHubSession(b, hubCfg, fake)
	for _, room := range rooms {
		s.joinedOK[room] = true
	}
	return s, fake
}

// TestNoticeEnvelopeSizeMatchesTheRealSend asserts the size the policy measures
// is the size the client actually emits, since one byte over the MDU is
// silently dropped by the link layer.
func TestNoticeEnvelopeSizeMatchesTheRealSend(t *testing.T) {
	t.Parallel()

	const text = "Commands: botinfo, dn, dnotice, dnoticecap, dnoticeme, help, ping, uptime, weather, whoami, wx"
	want, err := noticeEnvelopeSize(mustHex(replyOwnHash), "general", "gorrcbot", text)
	if err != nil {
		t.Fatalf("noticeEnvelopeSize: %v", err)
	}
	if want > rns.MDU {
		t.Fatalf("the reference reply measures %v bytes, larger than the MDU %v", want, rns.MDU)
	}

	// The size the policy measures must be the size the client really puts on
	// the wire, to the byte: one byte over the MDU is silently dropped.
	env := rrc.MakeClientEnvelope(rrc.TypeNotice, mustHex(replyOwnHash), []byte("general"),
		[]byte("gorrcbot"), text, make([]byte, 8), rrc.NowMs())
	data, err := rrc.EncodeEnvelope(env)
	if err != nil {
		t.Fatalf("EncodeEnvelope: %v", err)
	}
	if got := len(data); got != want {
		t.Errorf("noticeEnvelopeSize = %v, the client encodes %v for the same text", want, got)
	}
	// A longer text must measure strictly larger, which is the property the
	// splitter depends on.
	longer, err := noticeEnvelopeSize(mustHex(replyOwnHash), "general", "gorrcbot", text+"x")
	if err != nil {
		t.Fatalf("noticeEnvelopeSize(longer): %v", err)
	}
	if longer <= want {
		t.Errorf("a longer text measured %v, want more than %v", longer, want)
	}
}

// TestSplitNoticeTextFitsOneEnvelopeEach asserts a long reply is broken into
// chunks that each fit one envelope, so nothing is silently dropped.
func TestSplitNoticeTextFitsOneEnvelopeEach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{name: "short", text: "pong"},
		{name: "just under one envelope", text: strings.Repeat("x", 300)},
		{name: "one long line", text: strings.Repeat("y", 2000)},
		{name: "many short words", text: strings.Repeat("word ", 400)},
		{name: "multibyte line", text: strings.Repeat("é", 1200)},
		{name: "emoji line", text: strings.Repeat("🗿", 600)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			chunks, err := splitNoticeText(mustHex(replyOwnHash), "general", "gorrcbot", tt.text, 20)
			if err != nil {
				t.Fatalf("splitNoticeText: %v", err)
			}
			if len(chunks) == 0 {
				t.Fatal("splitNoticeText returned no chunks")
			}
			for i, chunk := range chunks {
				size, err := noticeEnvelopeSize(mustHex(replyOwnHash), "general", "gorrcbot", chunk)
				if err != nil {
					t.Fatalf("chunk %v: noticeEnvelopeSize: %v", i, err)
				}
				if size > rns.MDU {
					t.Errorf("chunk %v is %v bytes encoded, larger than the MDU %v", i, size, rns.MDU)
				}
			}
			// A text that fits one envelope is emitted untouched; a text that
			// does not is split.
			inputFits, err := noticeFits(mustHex(replyOwnHash), "general", "gorrcbot", tt.text)
			if err != nil {
				t.Fatalf("noticeFits: %v", err)
			}
			if inputFits {
				if len(chunks) != 1 || chunks[0] != tt.text {
					t.Errorf("chunks = %q, want the fitting text unchanged", chunks)
				}
				return
			}
			if len(chunks) < 2 {
				t.Errorf("a text that does not fit one envelope produced %v chunk(s)", len(chunks))
			}
		})
	}
}

// TestSplitNoticeTextIsRuneSafe asserts a split never produces invalid UTF-8.
func TestSplitNoticeTextIsRuneSafe(t *testing.T) {
	t.Parallel()

	chunks, err := splitNoticeText(mustHex(replyOwnHash), "general", "gorrcbot",
		strings.Repeat("🗿ab", 900), 10)
	if err != nil {
		t.Fatalf("splitNoticeText: %v", err)
	}
	for i, chunk := range chunks {
		for _, r := range chunk {
			if r == '\uFFFD' {
				t.Fatalf("chunk %v contains a replacement character: %q", i, chunk)
			}
		}
		if !utf8.ValidString(chunk) {
			t.Errorf("chunk %v is not valid UTF-8: %q", i, chunk)
		}
	}
}

// TestSplitNoticeTextMarksContinuations asserts the split is visible to a human
// reader instead of looking like the bot stopped mid-sentence.
func TestSplitNoticeTextMarksContinuations(t *testing.T) {
	t.Parallel()

	chunks, err := splitNoticeText(mustHex(replyOwnHash), "general", "gorrcbot",
		strings.Repeat("z", 1500), 20)
	if err != nil {
		t.Fatalf("splitNoticeText: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("chunks = %v, want the text split", len(chunks))
	}
	for i, chunk := range chunks[:len(chunks)-1] {
		if !strings.HasSuffix(chunk, splitMarker) {
			t.Errorf("chunk %v = %q, want it to end with the continuation marker %q", i, chunk, splitMarker)
		}
	}
}

// TestSplitNoticeTextTruncatesAtTheLineBudget asserts max_reply_lines bounds the
// reply and the truncation is visible.
func TestSplitNoticeTextTruncatesAtTheLineBudget(t *testing.T) {
	t.Parallel()

	chunks, err := splitNoticeText(mustHex(replyOwnHash), "general", "gorrcbot",
		strings.Repeat("w", 20000), 3)
	if err != nil {
		t.Fatalf("splitNoticeText: %v", err)
	}
	if len(chunks) != 3 {
		t.Fatalf("chunks = %v, want exactly the 3-line budget", len(chunks))
	}
	if !strings.HasSuffix(chunks[2], truncatedMarker) {
		t.Errorf("last chunk = %q, want it to end with %q", chunks[2], truncatedMarker)
	}
}

// TestSplitNoticeTextIsIdempotentForShortText asserts ordinary replies are never
// rewritten.
func TestSplitNoticeTextIsIdempotentForShortText(t *testing.T) {
	t.Parallel()

	const text = "Commands: botinfo, help, ping, uptime, whoami"
	chunks, err := splitNoticeText(mustHex(replyOwnHash), "general", "gorrcbot", text, 12)
	if err != nil {
		t.Fatalf("splitNoticeText: %v", err)
	}
	if len(chunks) != 1 || chunks[0] != text {
		t.Errorf("chunks = %q, want the text unchanged", chunks)
	}
}

// TestResponderSendsOneNoticePerLine asserts a three-line reply produces three
// in-room NOTICEs, each fitting one envelope.
func TestResponderSendsOneNoticePerLine(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, fake := newReplySession(t, cfg)
	calls := 0
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		calls++
		return []string{"line one", "line two", "line three"}
	})
	msg := addressedMessage("general", "@gorrcbot help")
	r.handle(session, msg)

	if calls != 1 {
		t.Fatalf("the command runner was called %v times, want 1", calls)
	}
	notices := fake.noticeList()
	if len(notices) != 3 {
		t.Fatalf("sent %v notices, want 3", len(notices))
	}
	for i, want := range []string{"line one", "line two", "line three"} {
		if notices[i].Text != want {
			t.Errorf("notice %v = %q, want %q", i, notices[i].Text, want)
		}
		if notices[i].Room != "general" {
			t.Errorf("notice %v room = %q, want general", i, notices[i].Room)
		}
	}
	if len(fake.directList()) != 0 {
		t.Errorf("direct notices = %v, want none for a hub without the capability", fake.directList())
	}
}

// TestResponderRoutesRepliesPerMode asserts the reply mode decides between a
// direct NOTICE and an in-room NOTICE.
func TestResponderRoutesRepliesPerMode(t *testing.T) {
	t.Parallel()

	requester := peerHashFor(0x31)
	tests := []struct {
		name       string
		mode       string
		capability bool
		known      bool
		wantDirect int
		wantRoom   int
	}{
		{name: "auto with capability and a known peer", mode: ReplyAuto, capability: true, known: true,
			wantDirect: 1},
		{name: "auto without the capability", mode: ReplyAuto, capability: false, known: true,
			wantRoom: 1},
		{name: "auto with an unknown peer", mode: ReplyAuto, capability: true, known: false,
			wantRoom: 1},
		{name: "direct forces a direct notice", mode: ReplyDirect, capability: true, known: true,
			wantDirect: 1},
		{name: "direct stays silent when impossible", mode: ReplyDirect, capability: false, known: true},
		{name: "direct stays silent for an unknown peer", mode: ReplyDirect, capability: true, known: false},
		{name: "room forces a room notice", mode: ReplyRoom, capability: true, known: true, wantRoom: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultTestConfig()
			cfg.Reply = tt.mode
			session, fake := newReplySession(t, cfg)
			if tt.capability {
				fake.setCapability(rrc.CapDirectNotice, true)
			}
			if tt.known {
				fake.setKnownPeer(hexString(requester), "Alice")
			}
			r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
				return []string{"pong"}
			})
			r.handle(session, addressedMessageFrom("general", "@gorrcbot ping", requester))

			if got := len(fake.directList()); got != tt.wantDirect {
				t.Errorf("direct notices = %v, want %v", got, tt.wantDirect)
			}
			if got := len(fake.noticeList()); got != tt.wantRoom {
				t.Errorf("room notices = %v, want %v", got, tt.wantRoom)
			}
		})
	}
}

// TestResponderSilentWithoutATrigger asserts an unaddressed message produces no
// output at all: gorrcbot is not a chatterbox.
func TestResponderSilentWithoutATrigger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
	}{
		{name: "bang help", text: "!help"},
		{name: "mention mid line", text: "hi @gorrcbot how are you"},
		{name: "nick in prose", text: "gorrcbot is down"},
		{name: "someone else", text: "@someoneelse help"},
		{name: "no word boundary", text: "@gorrcbotx help"},
		{name: "quoted mid line", text: `send "@gorrcbot help" to the room`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := defaultTestConfig()
			session, fake := newReplySession(t, cfg)
			called := 0
			r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
				called++
				return []string{"should not happen"}
			})
			r.handle(session, addressedMessage("general", tt.text))

			if called != 0 {
				t.Errorf("the command runner was called %v times for %q, want 0", called, tt.text)
			}
			if got := len(fake.noticeList()) + len(fake.directList()); got != 0 {
				t.Errorf("sent %v messages for %q, want silence", got, tt.text)
			}
		})
	}
}

// TestResponderIgnoresOwnMessages asserts the bot never answers itself,
// including the hub's fanout echo of its own notice.
func TestResponderIgnoresOwnMessages(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, fake := newReplySession(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"should not happen"}
	})
	// A room message and the fanout echo of our own notice, both carrying our
	// own identity hash.
	r.handle(session, &rrc.RRCMessage{Kind: "msg", Room: "general", Src: mustHex(replyOwnHash),
		Nick: "gorrcbot", Text: "@gorrcbot help"})
	r.handle(session, &rrc.RRCMessage{Kind: "notice", Room: "general", Src: mustHex(replyOwnHash),
		Nick: "gorrcbot", Text: "@gorrcbot help"})

	if got := len(fake.noticeList()) + len(fake.directList()); got != 0 {
		t.Errorf("sent %v messages for our own traffic, want silence", got)
	}
}

// TestResponderIgnoresUnjoinedRooms asserts a message in a room the bot never
// joined is dropped: the hub may fan out anything.
func TestResponderIgnoresUnjoinedRooms(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, fake := newReplySession(t, cfg, "general")
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"should not happen"}
	})
	r.handle(session, addressedMessage("somewhereelse", "@gorrcbot help"))

	if got := len(fake.noticeList()) + len(fake.directList()); got != 0 {
		t.Errorf("sent %v messages for an unjoined room, want silence", got)
	}
}

// TestResponderRejectsStaleMessages asserts a request that sat in flight is
// refused instead of answered out of order.
func TestResponderRejectsStaleMessages(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, fake := newReplySession(t, cfg)
	called := 0
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		called++
		return []string{"pong"}
	})
	now := time.Now()
	r.now = func() time.Time { return now }
	staleAsker := peerHashFor(0x81)

	stale := addressedMessageFrom("general", "@gorrcbot ping", staleAsker)
	stale.Ts = now.Add(-time.Minute).UnixMilli()
	r.handle(session, stale)

	if called != 0 {
		t.Errorf("the command runner was called for a stale request %v times, want 0", called)
	}
	notices := fake.noticeList()
	if len(notices) != 1 {
		t.Fatalf("sent %v notices for a stale request, want exactly 1 explanation", len(notices))
	}
	if !strings.Contains(notices[0].Text, "too old") {
		t.Errorf("stale reply = %q, want it to say the request is too old", notices[0].Text)
	}

	// A second stale request from the same identity inside the cooldown is
	// suppressed like every other reply.
	repeat := addressedMessageFrom("general", "@gorrcbot ping", staleAsker)
	repeat.Ts = stale.Ts
	r.handle(session, repeat)
	if got := len(fake.noticeList()); got != 1 {
		t.Errorf("sent %v notices for a repeated stale request, want 1", got)
	}

	// A fresh request from another identity is answered normally.
	fresh := addressedMessageFrom("general", "@gorrcbot ping", peerHashFor(0x82))
	fresh.Ts = now.UnixMilli()
	r.handle(session, fresh)
	if called != 1 {
		t.Errorf("the command runner was called %v times for a fresh request, want 1", called)
	}
}

// TestResponderStaleDirectNoticeIsAlsoRejected asserts the staleness rule is not
// limited to room traffic.
func TestResponderStaleDirectNoticeIsAlsoRejected(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, fake := newReplySession(t, cfg)
	now := time.Now()
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"pong"}
	})
	r.now = func() time.Time { return now }

	r.handle(session, &rrc.RRCMessage{Kind: "notice", Direct: true, Src: peerHashFor(0x41),
		Dst: mustHex(replyOwnHash), Text: "ping", Ts: now.Add(-time.Hour).UnixMilli()})

	if got := len(fake.directList()); got != 0 {
		t.Errorf("sent %v direct notices for a stale request, want 0", got)
	}
}

// TestResponderCooldownSuppressesBursts asserts a repeating requester cannot
// make the bot flood a room, and the cooldown expires.
func TestResponderCooldownSuppressesBursts(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.CooldownSecs = 8
	session, fake := newReplySession(t, cfg)
	now := time.Unix(1700000000, 0)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"pong"}
	})
	r.now = func() time.Time { return now }

	requester := peerHashFor(0x51)
	for range 5 {
		r.handle(session, addressedMessageFrom("general", "@gorrcbot ping", requester))
		now = now.Add(time.Second)
	}
	if got := len(fake.noticeList()); got != 1 {
		t.Errorf("sent %v notices during the cooldown window, want 1", got)
	}

	// Past the cooldown the same identity is answered again.
	now = now.Add(10 * time.Second)
	r.handle(session, addressedMessageFrom("general", "@gorrcbot ping", requester))
	if got := len(fake.noticeList()); got != 2 {
		t.Errorf("sent %v notices after the cooldown expired, want 2", got)
	}

	// A different identity is never affected by another's cooldown.
	other := peerHashFor(0x52)
	r.handle(session, addressedMessageFrom("general", "@gorrcbot ping", other))
	if got := len(fake.noticeList()); got != 3 {
		t.Errorf("sent %v notices after a second identity asked, want 3", got)
	}
}

// TestResponderCooldownIsBounded asserts the cooldown table cannot grow without
// limit: an always-on bot must survive a busy hub.
func TestResponderCooldownIsBounded(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, _ := newReplySession(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"pong"}
	})
	for i := range cooldownMaxEntries * 3 {
		peer := make([]byte, 16)
		peer[0] = byte(i % 256)
		peer[1] = byte(i / 256)
		r.handle(session, addressedMessageFrom("general", "@gorrcbot ping", peer))
	}
	r.mu.Lock()
	size := len(r.cooldown)
	r.mu.Unlock()
	if size > cooldownMaxEntries {
		t.Errorf("the cooldown table holds %v entries, want at most %v", size, cooldownMaxEntries)
	}
}

// TestResponderNeverDoubleRepliesToTheSameMessage asserts a duplicated delivery
// of one envelope produces one reply, not two.
func TestResponderNeverDoubleRepliesToTheSameMessage(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.CooldownSecs = 0
	session, fake := newReplySession(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"pong"}
	})

	msg := addressedMessage("general", "@gorrcbot ping")
	msg.ID = "deadbeefdeadbeef"
	r.handle(session, msg)
	r.handle(session, msg)
	r.handle(session, msg)

	if got := len(fake.noticeList()); got != 1 {
		t.Errorf("sent %v notices for one duplicated envelope, want 1", got)
	}
}

// TestResponderDuplicateGuardIsBounded asserts the duplicate guard cannot grow
// without limit either.
func TestResponderDuplicateGuardIsBounded(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.CooldownSecs = 0
	session, _ := newReplySession(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string { return nil })
	for i := range duplicateWindowEntries * 2 {
		msg := addressedMessage("general", "@gorrcbot ping")
		msg.ID = "id" + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i/676))
		r.handle(session, msg)
	}
	r.mu.Lock()
	size := len(r.seenIDs)
	r.mu.Unlock()
	if size > duplicateWindowEntries {
		t.Errorf("the duplicate guard holds %v entries, want at most %v", size, duplicateWindowEntries)
	}
}

// TestResponderSilenceIsAllowed asserts a command that returns no lines sends
// nothing rather than an empty NOTICE.
func TestResponderSilenceIsAllowed(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, fake := newReplySession(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string { return nil })
	r.handle(session, addressedMessage("general", "@gorrcbot help"))

	if got := len(fake.noticeList()) + len(fake.directList()); got != 0 {
		t.Errorf("sent %v messages for a silent command, want silence", got)
	}
}

// TestResponderTruncatesToTheLineBudget asserts max_reply_lines bounds one reply.
func TestResponderTruncatesToTheLineBudget(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.MaxReplyLines = 3
	session, fake := newReplySession(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"one", "two", "three", "four", "five"}
	})
	r.handle(session, addressedMessage("general", "@gorrcbot help"))

	notices := fake.noticeList()
	if len(notices) != 3 {
		t.Fatalf("sent %v notices, want the 3-line budget", len(notices))
	}
	if !strings.Contains(notices[2].Text, truncatedMarker) {
		t.Errorf("last notice = %q, want it to mark the truncation", notices[2].Text)
	}
}

// TestResponderSplitsLongLinesIntoEnvelopes asserts one long reply line becomes
// several MTU-sized NOTICEs rather than one dropped envelope.
func TestResponderSplitsLongLinesIntoEnvelopes(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.MaxReplyLines = 10
	session, fake := newReplySession(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{strings.Repeat("q", 1200)}
	})
	r.handle(session, addressedMessage("general", "@gorrcbot help"))

	notices := fake.noticeList()
	if len(notices) < 3 {
		t.Fatalf("sent %v notices for a 1200-byte line, want several", len(notices))
	}
	for i, notice := range notices {
		size, err := noticeEnvelopeSize(mustHex(replyOwnHash), "general", "gorrcbot", notice.Text)
		if err != nil {
			t.Fatalf("notice %v: %v", i, err)
		}
		if size > rns.MDU {
			t.Errorf("notice %v is %v bytes encoded, larger than the MDU %v", i, size, rns.MDU)
		}
	}
}

// TestResponderDropsRepliesForRoomsTheBotLeft asserts a reply is refused when
// the room is no longer joined, so the hub's rejection never becomes a mystery.
func TestResponderDropsRepliesForRoomsTheBotLeft(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, fake := newReplySession(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"pong"}
	})
	// The room is gone by the time the reply is composed.
	session.mu.Lock()
	session.joinedOK = map[string]bool{}
	session.mu.Unlock()
	r.handle(session, addressedMessage("general", "@gorrcbot ping"))

	if got := len(fake.noticeList()); got != 0 {
		t.Errorf("sent %v notices for a room the bot is no longer in, want 0", got)
	}
}

// TestResponderNeverRepliesBeforeWelcome asserts nothing is sent while the hub
// is still connecting.
func TestResponderNeverRepliesBeforeWelcome(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, fake := newReplySession(t, cfg)
	fake.setStatus(rrc.StatusConnecting)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"pong"}
	})
	r.handle(session, addressedMessage("general", "@gorrcbot ping"))

	if got := len(fake.noticeList()) + len(fake.directList()); got != 0 {
		t.Errorf("sent %v messages before WELCOME, want silence", got)
	}
}

// TestResponderPassesTheMatchedTriggerToTheCommandLayer asserts the command
// layer receives the trigger details it needs for usage messages.
func TestResponderPassesTheMatchedTriggerToTheCommandLayer(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	session, _ := newReplySession(t, cfg)
	var got *commandRequest
	r := newResponder(cfg, mustHex(replyOwnHash), func(req *commandRequest) []string {
		got = req
		return nil
	})
	requester := peerHashFor(0x61)
	r.handle(session, addressedMessageFrom("general", "@gorrcbot dnotice alice hi there", requester))

	if got == nil {
		t.Fatal("the command runner was never called")
	}
	if got.Command != "dnotice alice hi there" {
		t.Errorf("Command = %q, want the full command line", got.Command)
	}
	if got.Room != "general" {
		t.Errorf("Room = %q, want general", got.Room)
	}
	if got.Nick != "gorrcbot" {
		t.Errorf("Nick = %q, want the matched trigger nick", got.Nick)
	}
	if got.Session != session {
		t.Error("Session is not the session the message arrived on")
	}
	if hexString(got.Msg.Src) != hexString(requester) {
		t.Errorf("Msg.Src = %v, want the requester hash", hexString(got.Msg.Src))
	}
}

// TestResponderReconnectDoesNotDropReplies asserts a reply composed after a
// reconnect is still delivered: the policy has no state that a new link breaks.
func TestResponderReconnectDoesNotDropReplies(t *testing.T) {
	t.Parallel()

	cfg := defaultTestConfig()
	cfg.CooldownSecs = 0
	session, fake := newReplySession(t, cfg)
	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"pong"}
	})
	requester := peerHashFor(0x71)
	r.handle(session, addressedMessageFrom("general", "@gorrcbot ping", requester))

	// Reconnect: the session state is re-armed and the room confirmed again.
	session.onLinkEstablished()
	session.observeJoin(&rrc.RRCMessage{Kind: "system", Room: "general"})
	r.handle(session, addressedMessageFrom("general", "@gorrcbot ping", requester))

	if got := len(fake.noticeList()); got != 2 {
		t.Errorf("sent %v notices across a reconnect, want 2", got)
	}
}

// TestResponderFallsBackToTheRoomWhenTheDirectSendFails asserts a direct NOTICE
// that cannot be sent does not lose the reply: in auto mode the answer still
// goes to the room, and it goes exactly once. The client validates the
// destination, the capability, and the envelope size before it hands anything
// to the link, so a failed direct send has delivered nothing and the fallback
// cannot duplicate it.
func TestResponderFallsBackToTheRoomWhenTheDirectSendFails(t *testing.T) {
	t.Parallel()

	requester := peerHashFor(0x41)
	cfg := defaultTestConfig()
	cfg.Reply = ReplyAuto
	session, fake := newReplySession(t, cfg)
	fake.setCapability(rrc.CapDirectNotice, true)
	fake.setKnownPeer(hexString(requester), "Alice")
	fake.setDirectErr(rrc.ErrDestinationNotConnected)

	r := newResponder(cfg, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"pong"}
	})
	r.handle(session, addressedMessageFrom("general", "@gorrcbot ping", requester))

	if got := len(fake.directList()); got != 0 {
		t.Errorf("direct notices = %v, want none", got)
	}
	notices := fake.noticeList()
	if len(notices) != 1 || notices[0].Text != "pong" {
		t.Errorf("room fallback = %v, want exactly one %q", notices, "pong")
	}

	// The mode that demands a private answer still stays silent rather than
	// publishing the reply in the room.
	strict := defaultTestConfig()
	strict.Reply = ReplyDirect
	session, fake = newReplySession(t, strict)
	fake.setCapability(rrc.CapDirectNotice, true)
	fake.setKnownPeer(hexString(requester), "Alice")
	fake.setDirectErr(rrc.ErrDestinationNotConnected)
	r = newResponder(strict, mustHex(replyOwnHash), func(*commandRequest) []string {
		return []string{"pong"}
	})
	r.handle(session, addressedMessageFrom("general", "@gorrcbot ping", requester))

	if got := len(fake.noticeList()); got != 0 {
		t.Errorf("room notices = %v, want none with reply = %q", got, ReplyDirect)
	}
	if got := len(fake.directList()); got != 0 {
		t.Errorf("direct notices = %v, want none", got)
	}
}
