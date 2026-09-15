// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the reply policy: how an addressed command becomes output.
//
// The RRC link layer silently drops an envelope larger than the MDU, so every
// reply is emitted as one NOTICE per line and each line is measured. A line too
// long for one envelope is split on a rune boundary and every continuation is
// marked, so a reader can see the bot did not simply stop mid-sentence. The
// reply travels as a direct NOTICE (K_DST) only when the request itself arrived
// that way, because a room request has to be answered where the asker can read
// it: a standard rrcd hub advertises its own capabilities in WELCOME and never
// publishes the capabilities another client announced in HELLO, so no bot can
// learn whether a requester could display a private answer. Two guards keep an
// always-on bot from becoming a nuisance: a per-requester cooldown, and a
// duplicate-envelope window so a redelivered message is never answered twice.

package main

import (
	"bytes"
	"strings"
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// Reply shaping constants.
const (
	// splitMarker marks a chunk that continues in the next envelope.
	splitMarker = " …"
	// truncatedMarker marks a reply cut short by max_reply_lines.
	truncatedMarker = " … [truncated]"
	// staleAfter is how long an addressed message stays answerable. A request
	// that waited in flight longer than this is refused, because the answer
	// would arrive long after the asker moved on.
	staleAfter = 30 * time.Second
	// staleReply is the single line sent for a message that is too old.
	staleReply = "too old, ask again"
)

// Guard sizes. Both tables are bounded so a busy hub cannot grow the bot's
// memory without limit.
const (
	// cooldownMaxEntries bounds the per-requester cooldown table.
	cooldownMaxEntries = 256
	// duplicateWindowEntries bounds the recently-answered message-id window.
	duplicateWindowEntries = 512
)

// commandRequest is one addressed command the bot must answer.
type commandRequest struct {
	// Session is the hub session the request arrived on.
	Session *hubSession
	// Msg is the message that carried the request.
	Msg *rrc.RRCMessage
	// Room is the room the request arrived in, empty for a direct notice.
	Room string
	// Command is the command line with the address removed and trimmed.
	Command string
	// Direct reports that the request arrived as a direct NOTICE.
	Direct bool
	// Nick is the trigger nick that matched, or "" for a hash-prefix or direct
	// address.
	Nick string
	// Now is the time the request is being handled, used by commands that
	// report durations.
	Now time.Time
}

// commandRunner produces the reply lines for one addressed command. Returning
// no lines means the bot stays silent. The command registry implements it.
type commandRunner func(*commandRequest) []string

// responder is the reply policy state for one bot run.
type responder struct {
	cfg     *BotConfig
	ownHash []byte
	run     commandRunner
	// now is the clock, injectable so the cooldown and staleness rules are
	// tested without waiting on real time.
	now func() time.Time

	mu        sync.Mutex
	cooldown  map[string]time.Time
	seenIDs   map[string]struct{}
	seenOrder []string
}

// newResponder builds the reply policy around a command runner.
func newResponder(cfg *BotConfig, ownHash []byte, run commandRunner) *responder {
	return &responder{
		cfg:      cfg,
		ownHash:  ownHash,
		run:      run,
		now:      time.Now,
		cooldown: make(map[string]time.Time),
		seenIDs:  make(map[string]struct{}),
	}
}

// handle applies the whole policy to one inbound message. It never returns an
// error: every failure to reply is silent by contract, and a failure to send is
// logged, because a bot that reports its own problems into a chat room would be
// worse than one that stays quiet.
func (r *responder) handle(s *hubSession, msg *rrc.RRCMessage) {
	if s == nil || msg == nil {
		return
	}
	// Our own composition, including the hub's fanout echo of it.
	if len(r.ownHash) > 0 && bytes.Equal(msg.Src, r.ownHash) {
		return
	}
	// Nothing is answered before the hub has welcomed us: the rooms are not
	// joined yet and the WELCOME limits are unknown.
	if s.conn.GetHubStatus() != rrc.StatusConnected {
		return
	}

	room := normalizeRoom(msg.Room)
	if !msg.Direct && !s.isJoined(room) {
		return
	}

	trig := parseTrigger(msg, r.cfg.TriggerNick(s.cfg, room), hexString(r.ownHash))
	if !trig.Addressed {
		return
	}

	now := r.now()
	if msg.ID != "" && !r.markSeen(msg.ID) {
		return
	}
	requester := hexString(msg.Src)
	logf("request from %v in %q (age %v): %q", requester, room, msgAge(msg, now),
		trig.Command)

	if r.isStale(msg, now) {
		if !r.admit(requester, now) {
			logf("suppressed the request from %v: the %vs cooldown is still running", requester, r.cfg.CooldownSecs)
			return
		}
		r.send(s, room, msg, r.directRoute(s, msg, trig.Direct), []string{staleReply})
		return
	}

	if !r.admit(requester, now) {
		// Without this line a suppressed request looks exactly like a bot that
		// ignored the asker: the request line above is the only other trace.
		logf("suppressed the request from %v: the %vs cooldown is still running", requester, r.cfg.CooldownSecs)
		return
	}

	lines := r.runCommand(&commandRequest{
		Session: s,
		Msg:     msg,
		Room:    room,
		Command: trig.Command,
		Direct:  trig.Direct,
		Nick:    trig.Nick,
		Now:     now,
	})
	r.send(s, room, msg, r.directRoute(s, msg, trig.Direct), lines)
}

// runCommand executes one command through the runner, recovering from a panic so
// one bad command cannot take the bot down.
func (r *responder) runCommand(req *commandRequest) (lines []string) {
	if r.run == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			logf("recovered while running %q: %v", req.Command, recovered)
			lines = []string{"internal error — try again"}
		}
	}()
	return r.run(req)
}

// msgAge renders how long ago a message was stamped, for the log. A message
// with no usable timestamp has no age.
func msgAge(msg *rrc.RRCMessage, now time.Time) time.Duration {
	if msg == nil || msg.Ts <= 0 {
		return 0
	}
	return now.Sub(time.UnixMilli(msg.Ts)).Round(time.Second)
}

// isStale reports whether a message waited too long to be worth answering.
func (r *responder) isStale(msg *rrc.RRCMessage, now time.Time) bool {
	if msg.Ts <= 0 {
		return false
	}
	return now.Sub(time.UnixMilli(msg.Ts)) > staleAfter
}

// markSeen records a message id and reports whether it had not been answered
// yet. It runs the bounded window so a long session cannot grow it without
// limit.
func (r *responder) markSeen(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seenIDs[id]; ok {
		return false
	}
	r.seenIDs[id] = struct{}{}
	r.seenOrder = append(r.seenOrder, id)
	if len(r.seenOrder) > duplicateWindowEntries {
		evict := r.seenOrder[0]
		r.seenOrder = r.seenOrder[1:]
		delete(r.seenIDs, evict)
	}
	return true
}

// admit applies the per-requester cooldown and records the reply. It returns
// false when the requester asked again inside the cooldown window.
func (r *responder) admit(requester string, now time.Time) bool {
	if requester == "" {
		return true
	}
	cooldown := time.Duration(r.cfg.CooldownSecs * float64(time.Second))
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, ok := r.cooldown[requester]; ok && now.Sub(last) < cooldown {
		return false
	}
	r.gcCooldownLocked(now, cooldown)
	r.cooldown[requester] = now
	return true
}

// gcCooldownLocked drops expired cooldown entries, and then the oldest ones, so
// the table stays bounded.
func (r *responder) gcCooldownLocked(now time.Time, cooldown time.Duration) {
	if len(r.cooldown) < cooldownMaxEntries {
		return
	}
	for key, last := range r.cooldown {
		if now.Sub(last) >= cooldown {
			delete(r.cooldown, key)
		}
	}
	for len(r.cooldown) >= cooldownMaxEntries {
		oldestKey, oldest := "", now
		for key, last := range r.cooldown {
			if oldestKey == "" || last.Before(oldest) {
				oldestKey, oldest = key, last
			}
		}
		delete(r.cooldown, oldestKey)
	}
}

// send emits the reply lines as one NOTICE each, along the route the policy
// chose. A reply is dropped when the room it belongs to is no longer joined,
// because the hub would reject it anyway.
func (r *responder) send(s *hubSession, room string, msg *rrc.RRCMessage, direct bool, lines []string) {
	if len(lines) == 0 {
		return
	}
	if room != "" && !s.isJoined(room) {
		return
	}
	nick := r.cfg.AdvertisedNick(s.cfg)
	chunks := r.chunksFor(room, nick, lines)
	if len(chunks) == 0 {
		return
	}

	if !direct {
		if r.cfg.Reply == ReplyDirect {
			// The mode asks for a direct notice and this hub cannot deliver one.
			logf("no direct-notice route to %v; reply dropped (reply = %q)",
				hexString(msg.Src), ReplyDirect)
			return
		}
		if room == "" {
			logf("no reply route for the direct request from %v", hexString(msg.Src))
			return
		}
		for _, chunk := range chunks {
			if _, err := s.conn.SendNotice(room, chunk); err != nil {
				logf("notice in %q failed: %v", room, err)
				return
			}
		}
		logf("replied to %v in %q with %v notice(s)", hexString(msg.Src), room, len(chunks))
		return
	}

	// The route was chosen before anything was sent, so a direct reply either
	// delivers every chunk or nothing: SendDirectNotice validates the
	// destination, the capability, and the envelope size before it hands
	// anything to the link.
	if err := r.sendDirect(s, chunks, msg); err != nil {
		logf("direct notice to %v failed: %v", hexString(msg.Src), err)
		return
	}
	logf("replied to %v with %v direct notice(s)", hexString(msg.Src), len(chunks))
}

// sendDirect delivers every chunk as a direct NOTICE and returns the first
// failure. Nothing is sent once an error is returned, because the client
// validates the destination, the capability, and the envelope size before it
// hands anything to the link.
func (r *responder) sendDirect(s *hubSession, chunks []string, msg *rrc.RRCMessage) error {
	for _, chunk := range chunks {
		if err := s.conn.SendDirectNotice(msg.Src, chunk); err != nil {
			return err
		}
	}
	return nil
}

// directRoute reports whether this reply travels as a direct NOTICE (K_DST).
// The requester's own capabilities should decide that, but a standard rrcd hub
// never publishes them: WELCOME carries the hub's capability set, which is the
// same for every client, while a peer's HELLO capability map stays in the hub's
// session table. The one observable proof that a requester speaks the K_DST
// extension is that its request arrived as a direct NOTICE, so auto mode answers
// a room request in the room, which is the only route the asker is known to be
// able to read. An explicit reply = "direct" overrides that, and stays silent
// when the hub cannot deliver.
func (r *responder) directRoute(s *hubSession, msg *rrc.RRCMessage, requesterDirect bool) bool {
	if r.cfg.Reply == ReplyRoom {
		return false
	}
	if len(msg.Src) != rrc.IdentityHashLen {
		return false
	}
	if !s.conn.HasCapability(rrc.CapDirectNotice) {
		return false
	}
	if r.cfg.Reply == ReplyAuto && !requesterDirect {
		return false
	}
	// A direct NOTICE rides the hub link: the hub forwards an envelope whose
	// K_DST names the peer, so the client needs no path to the peer itself,
	// only the hub's knowledge of it.
	return s.knowsPeer(hexString(msg.Src))
}

// chunksFor turns the reply lines into the envelopes they will be sent as: one
// chunk per line, long lines split, and the whole reply bounded by
// max_reply_lines.
func (r *responder) chunksFor(room, nick string, lines []string) []string {
	budget := max(r.cfg.MaxReplyLines, 1)
	chunks := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		split, err := splitNoticeText(r.ownHash, room, nick, line, budget)
		if err != nil {
			logf("could not fit a reply into envelopes: %v", err)
			continue
		}
		chunks = append(chunks, split...)
	}
	if len(chunks) > budget {
		last := trimToFit(r.ownHash, room, nick, chunks[budget-1], truncatedMarker)
		chunks = append(chunks[:budget-1], last)
	}
	return chunks
}

// noticeEnvelopeSize returns the encoded size of the notice the client would
// send for text. The message id is always eight bytes and the timestamp is
// always milliseconds, so measuring with those shapes is exact.
func noticeEnvelopeSize(ownHash []byte, room, nick, text string) (int, error) {
	env := rrc.MakeClientEnvelope(rrc.TypeNotice, ownHash, []byte(room), []byte(nick),
		text, make([]byte, 8), rrc.NowMs())
	data, err := rrc.EncodeEnvelope(env)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// splitNoticeText splits text into chunks that each fit one envelope. Chunks
// other than the last end with splitMarker, and a chunk cut off by the line
// budget ends with truncatedMarker. Splits always land on a rune boundary.
func splitNoticeText(ownHash []byte, room, nick, text string, maxLines int) ([]string, error) {
	if text == "" {
		return nil, nil
	}
	if maxLines < 1 {
		maxLines = 1
	}
	if fits, err := noticeFits(ownHash, room, nick, text); err != nil {
		return nil, err
	} else if fits {
		return []string{text}, nil
	}

	var chunks []string
	remaining := text
	for len(chunks) < maxLines {
		if fits, err := noticeFits(ownHash, room, nick, remaining); err != nil {
			return nil, err
		} else if fits {
			chunks = append(chunks, remaining)
			return chunks, nil
		}
		// The line budget is exhausted by this chunk: cut it to the marker
		// that says the rest was dropped.
		if len(chunks) == maxLines-1 {
			chunks = append(chunks, trimToFit(ownHash, room, nick, remaining, truncatedMarker))
			return chunks, nil
		}
		head, tail, err := splitPrefix(ownHash, room, nick, remaining, splitMarker)
		if err != nil {
			return nil, err
		}
		if head == "" {
			// Even the marker alone will not fit; emit the best truncation.
			chunks = append(chunks, trimToFit(ownHash, room, nick, remaining, truncatedMarker))
			return chunks, nil
		}
		chunks = append(chunks, head+splitMarker)
		remaining = tail
	}
	return chunks, nil
}

// noticeFits reports whether text fits one notice envelope.
func noticeFits(ownHash []byte, room, nick, text string) (bool, error) {
	size, err := noticeEnvelopeSize(ownHash, room, nick, text)
	if err != nil {
		return false, err
	}
	return size <= rns.MDU, nil
}

// trimToFit returns the longest rune-aligned prefix of text whose length plus
// marker fits one envelope. The marker is dropped when even that cannot fit.
func trimToFit(ownHash []byte, room, nick, text, marker string) string {
	if fits, err := noticeFits(ownHash, room, nick, text+marker); err == nil && fits {
		return text + marker
	}
	head, _, err := splitPrefix(ownHash, room, nick, text, marker)
	if err != nil {
		return ""
	}
	if head == "" {
		return ""
	}
	return head + marker
}

// splitPrefix finds the longest rune-aligned prefix of text that still fits one
// envelope once marker is appended, and returns it with the untouched remainder.
// A small tail shorter than a marker is folded into the head so the remainder
// never ends up empty.
func splitPrefix(ownHash []byte, room, nick, text, marker string) (string, string, error) {
	runes := []rune(text)
	// Binary search the largest rune count that fits.
	lo, hi := 0, len(runes)
	best := -1
	for lo <= hi {
		mid := (lo + hi) / 2
		fits, err := noticeFits(ownHash, room, nick, string(runes[:mid])+marker)
		if err != nil {
			return "", "", err
		}
		if fits {
			best = mid
			lo = mid + 1
			continue
		}
		hi = mid - 1
	}
	if best <= 0 {
		return "", text, nil
	}
	// Do not leave a remainder that is only whitespace: it would produce an
	// empty-looking continuation line.
	remainder := string(runes[best:])
	if strings.TrimSpace(remainder) == "" {
		return string(runes[:best]) + strings.TrimSpace(remainder), "", nil
	}
	return string(runes[:best]), remainder, nil
}
