// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the catchup command: the answer to the question a client that
// has been offline asks first. It merges the hub's live room buffer with the
// history this bot itself persisted, so a digest covers the room's recent
// conversation even when the bot restarted in between.
//
// The digest is bounded twice over: the rows it will consider, and the NOTICE
// lines it may produce. Every quoted row is attacker-supplied text, so it is
// stripped of terminal escapes and shortened before it is repeated under the
// bot's name (see safeEcho).

package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

const (
	// catchupUsage is the usage line for the catchup command.
	catchupUsage = "catchup [room] [window]"
	// catchupDefaultWindow is how far back a bare catchup looks when the asker
	// has never spoken in the room.
	catchupDefaultWindow = 2 * time.Hour
	// catchupMaxWindow bounds an explicit window, so a typo cannot ask for the
	// whole history.
	catchupMaxWindow = 7 * 24 * time.Hour
	// maxCatchupTextBytes bounds the text of one quoted row.
	maxCatchupTextBytes = 120
	// maxCatchupRows bounds how many rows one digest considers, live buffer and
	// persisted history together.
	maxCatchupRows = 500
	// maxCatchupRoomsNamed bounds how many joined rooms the unknown-room answer
	// lists, so one reply stays one line.
	maxCatchupRoomsNamed = 8
	// catchupClockLayout renders a time within the last day.
	catchupClockLayout = "15:04"
	// catchupDateLayout renders an older time.
	catchupDateLayout = "2006-01-02 15:04"
)

// runCatchup answers "what did I miss" for one room: the conversation since the
// asker last spoke, or since an explicit window.
func (c *commandContext) runCatchup() []string {
	now := c.req.Now
	if now.IsZero() {
		now = time.Now()
	}
	room, window, explicit, rejected := c.catchupTarget()
	if rejected != nil {
		return rejected
	}

	rows := c.conversationRows(room)
	botNick := c.reg.advertisedNick(c.session())
	asker := hexString(c.req.Msg.Src)
	since := now.Add(-catchupDefaultWindow)
	if explicit {
		since = now.Add(-window)
	} else if ts := catchupAnchor(rows, asker, c.req.Msg.ID, botNick); ts > 0 {
		since = time.UnixMilli(ts)
	}

	filter := digestFilter{
		ownHex:   c.reg.identityHex(),
		askerHex: asker,
		botNick:  botNick,
		skipID:   c.req.Msg.ID,
	}
	lines := catchupLines(room, digestRows(rows, since, filter), since, now, c.replyBudget())
	if lines == nil {
		return []string{fmt.Sprintf("%v has been quiet since %v", room, formatClock(since, now))}
	}
	return lines
}

// catchupTarget resolves the room and the window from the arguments. A rejected
// argument returns the reply lines that explain why, never a fetch or a guess.
func (c *commandContext) catchupTarget() (room string, window time.Duration, explicit bool, rejected []string) {
	var roomToken string
	for token := range strings.FieldsSeq(c.Args) {
		// A duration-shaped token is a window unless it names a joined room,
		// so a room called "2h" stays reachable by name.
		if d, ok := parseWindow(token); ok && !c.joinedRoom(token) {
			if explicit {
				return "", 0, false, []string{"Usage: " + catchupUsage}
			}
			window, explicit = d, true
			continue
		}
		if roomToken != "" {
			return "", 0, false, []string{"Usage: " + catchupUsage}
		}
		roomToken = strings.TrimPrefix(token, "#")
		if roomToken == "" {
			return "", 0, false, []string{"Usage: " + catchupUsage}
		}
	}

	switch {
	case roomToken != "":
		joined, ok := c.joinedRoomName(roomToken)
		if !ok {
			return "", 0, false, []string{fmt.Sprintf("no room named %q: I have joined %v",
				safeEcho(roomToken, maxEchoNickBytes), c.joinedRoomSummary())}
		}
		room = joined
	case c.req.Room != "":
		room = normalizeRoom(c.req.Room)
	default:
		// A direct request carries no room, so the bot falls back to the first
		// room it was configured to join: predictable, and the same room a
		// mention in the room would have used.
		room = c.firstRoom()
		if room == "" {
			return "", 0, false, []string{"I have not joined a room yet, so there is nothing to catch up on"}
		}
	}
	if !persistableRoomName(room) {
		return "", 0, false, []string{"Usage: " + catchupUsage}
	}
	return room, window, explicit, nil
}

// joinedRoom reports whether the token names a room the bot has joined, by the
// name the hub uses, ignoring a leading '#'.
func (c *commandContext) joinedRoom(token string) bool {
	_, ok := c.joinedRoomName(token)
	return ok
}

// joinedRoomName maps a room token to the hub's own name for that room.
func (c *commandContext) joinedRoomName(token string) (string, bool) {
	want := normalizeRoom(strings.TrimPrefix(token, "#"))
	for _, room := range c.conn().JoinedRoomList() {
		if normalizeRoom(room) == want {
			return normalizeRoom(room), true
		}
	}
	return "", false
}

// joinedRoomSummary lists the joined rooms for an unknown-room answer, bounded to
// one line.
func (c *commandContext) joinedRoomSummary() string {
	rooms := c.conn().JoinedRoomList()
	names := make([]string, 0, len(rooms))
	for _, room := range rooms {
		if name := safeEcho(normalizeRoom(room), maxEchoNickBytes); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > maxCatchupRoomsNamed {
		names = append(names[:maxCatchupRoomsNamed], "…")
	}
	return strings.Join(names, ", ")
}

// firstRoom is the room a direct request falls back to: the first room this hub
// was configured to join, else the first room the hub reports.
func (c *commandContext) firstRoom() string {
	if session := c.session(); session != nil && session.cfg != nil {
		for _, room := range session.cfg.Rooms {
			if name := normalizeRoom(room.Name); persistableRoomName(name) {
				return name
			}
		}
	}
	for _, room := range c.conn().JoinedRoomList() {
		if name := normalizeRoom(room); persistableRoomName(name) {
			return name
		}
	}
	return ""
}

// conversationRows returns the rows of one room, live buffer first and persisted
// history second, so a digest survives a bot restart.
func (c *commandContext) conversationRows(room string) []*rrc.RRCMessage {
	rows := append([]*rrc.RRCMessage(nil), c.conn().GetMessages(room)...)
	if store := c.reg.historyStore(c.session()); store != nil {
		rows = append(rows, store.newest(room, maxCatchupRows)...)
	}
	return rows
}

// catchupAnchor reports when the asker was last a participant in the room. Their
// own command lines are not "something I said": the catchup they just typed is a
// room row by the time the bot reads the room, and anchoring on it would answer
// "nothing happened" every time. Rows addressed to the bot by nick are skipped
// for the same reason, so a second catchup digests what happened since the
// asker's last real remark.
func catchupAnchor(rows []*rrc.RRCMessage, askerHex, skipID, botNick string) int64 {
	var latest int64
	for _, row := range rows {
		if row == nil || hexString(row.Src) != askerHex || row.ID == skipID {
			continue
		}
		if addressedToNick(row.Text, botNick) {
			continue
		}
		if row.Ts > latest {
			latest = row.Ts
		}
	}
	return latest
}

// addressedToNick reports whether a row's text opens by addressing the bot by
// nick, which makes it a command rather than conversation.
func addressedToNick(text, nick string) bool {
	if nick == "" {
		return false
	}
	trimmed := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(trimmed, "@"+strings.ToLower(nick))
}

// digestFilter is who is asking, so a digest leaves out what that asker already
// knows first hand: the bot's own replies, the command being answered, and the
// asker's own earlier commands.
type digestFilter struct {
	// ownHex is the bot's own identity hash.
	ownHex string
	// askerHex is the requester's identity hash.
	askerHex string
	// botNick is the nick a command in this room addresses.
	botNick string
	// skipID is the message id of the command being answered.
	skipID string
}

// digestRows keeps the conversation that happened since the cutoff, newest
// first, without duplicates: the hub fans a message out once per member, so the
// live buffer and the history file can both hold the same row.
func digestRows(rows []*rrc.RRCMessage, since time.Time, filter digestFilter) []*rrc.RRCMessage {
	cutoff := since.UnixMilli()
	seen := make(map[string]bool, len(rows))
	out := make([]*rrc.RRCMessage, 0, len(rows))
	for _, row := range rows {
		if !digestable(row, filter.ownHex) || row.Ts < cutoff {
			continue
		}
		if filter.skipID != "" && row.ID == filter.skipID {
			continue
		}
		if hexString(row.Src) == filter.askerHex && addressedToNick(row.Text, filter.botNick) {
			continue
		}
		key := row.ID
		if key == "" {
			key = fmt.Sprintf("%v|%v|%v", row.Ts, row.Nick, row.Text)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ts > out[j].Ts })
	return out
}

// digestable reports whether a row belongs in a digest: speech and actions, plus
// replies attributed to a peer by nick (a bot's answer is conversation too), but
// never hub chatter, the standing greeting, an undated row, or the bot's own
// lines.
func digestable(row *rrc.RRCMessage, ownHex string) bool {
	if row == nil || row.Pinned || row.Ts <= 0 {
		return false
	}
	switch row.Kind {
	case "msg", "action":
	case "notice":
		if !row.IsConversation() {
			return false
		}
	default:
		return false
	}
	return ownHex == "" || hexString(row.Src) != ownHex
}

// catchupLines renders the digest within the reply budget: a header, as many
// rows as fit, and a trailer that admits what was left out. It returns nil when
// no row renders, so the caller answers that the room has been quiet.
func catchupLines(room string, rows []*rrc.RRCMessage, since, now time.Time, budget int) []string {
	rendered := make([]string, 0, len(rows))
	speakers := make(map[string]bool, len(rows))
	for _, row := range rows {
		line := digestLine(row, now)
		if line == "" {
			// A row that sanitizes away is not a message the caller will see,
			// so it is not counted either.
			continue
		}
		speakers[digestSpeaker(row.Nick)] = true
		rendered = append(rendered, line)
	}
	if len(rendered) == 0 {
		return nil
	}

	header := fmt.Sprintf("catchup in %v: %v from %v since %v (%v ago)", room,
		pluralCount(len(rendered), "message", "messages"),
		pluralCount(len(speakers), "speaker", "speakers"),
		formatClock(since, now), formatAge(now.Sub(since)))

	maxRows := max(budget, 1) - 1
	if len(rendered) <= maxRows {
		return append([]string{header}, rendered...)
	}
	if maxRows <= 0 {
		// No room for a body line: the header still answers the question.
		return []string{header}
	}
	// One line of the budget goes to the trailer, which counts the messages the
	// caller will not see.
	shown := maxRows - 1
	out := append([]string{header}, rendered[:shown]...)
	return append(out, fmt.Sprintf("… %v earlier messages not shown", len(rendered)-shown))
}

// digestLine renders one digest row: a clock (or a date when the row is older
// than a day), the speaker, and the quoted text.
func digestLine(row *rrc.RRCMessage, now time.Time) string {
	text := safeEcho(row.Text, maxCatchupTextBytes)
	if text == "" {
		return ""
	}
	return fmt.Sprintf("[%v] %v: %v", formatClock(time.UnixMilli(row.Ts), now),
		digestSpeaker(row.Nick), text)
}

// digestSpeaker renders a nick for a digest line, falling back to a word rather
// than an empty column when a nick is missing or sanitizes away.
func digestSpeaker(nick string) string {
	if name := safeEcho(normalizeNick(nick), maxEchoNickBytes); name != "" {
		return name
	}
	return "someone"
}

// normalizeNick trims a nick the way the client does before it is displayed.
func normalizeNick(nick string) string {
	return strings.TrimSpace(nick)
}

// formatClock renders a time as a clock for the last day, and as a date and
// clock for anything older, so a digest line is never ambiguous. Every time is
// rendered in the asker's own reference frame — the location of the clock the
// command arrived with — so a header and the rows under it always agree, even
// when one came from a stored millisecond timestamp.
func formatClock(t, now time.Time) string {
	t = t.In(now.Location())
	if t.After(now) || now.Sub(t) < 24*time.Hour {
		return t.Format(catchupClockLayout)
	}
	return t.Format(catchupDateLayout)
}

// parseWindow parses a window argument such as "15m", "2h" or "1d", clamped to
// catchupMaxWindow. Anything else is not a window.
func parseWindow(token string) (time.Duration, bool) {
	if len(token) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(token[:len(token)-1])
	if err != nil || n <= 0 {
		return 0, false
	}
	var unit time.Duration
	switch token[len(token)-1] {
	case 's':
		unit = time.Second
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	default:
		return 0, false
	}
	if n > int(catchupMaxWindow/unit) {
		return catchupMaxWindow, true
	}
	return time.Duration(n) * unit, true
}

// replyBudget is how many NOTICE lines one reply may produce, which is the
// operator's max_reply_lines setting.
func (c *commandContext) replyBudget() int {
	if c.reg.bot == nil || c.reg.bot.cfg == nil {
		return DefaultMaxReplyLines
	}
	return max(c.reg.bot.cfg.MaxReplyLines, 1)
}
