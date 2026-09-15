// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the search command: "where was that link?". It reads the same
// two sources catchup reads — the hub's live room buffer and the history this bot
// persisted — so a match survives a bot restart, and it never scans more than a
// bounded number of rooms or rows.
//
// A search term and every quoted match are attacker-supplied text, so the term is
// stripped and validated before it is used or echoed, and each reported row is
// stripped and shortened before it is repeated under the bot's name (see
// safeEcho).

package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gmlewis/go-reticulum/rrc"
)

const (
	// searchUsage is the usage line for the search command.
	searchUsage = "search <term> [#room]"
	// maxSearchTermBytes bounds a search term. It is deliberately small: the term
	// is echoed back, and a term longer than this is not a search.
	maxSearchTermBytes = 32
	// maxSearchTextBytes bounds the text of one reported match.
	maxSearchTextBytes = 120
	// maxSearchRooms bounds how many joined rooms one search reads, so a bot in
	// many rooms cannot be made to scan them all with one line.
	maxSearchRooms = 16
	// maxSearchMatches bounds how many matches one search collects, newest
	// first, before it stops reading.
	maxSearchMatches = 200
)

// runSearch reports where a term appeared in the rooms the bot has joined: the
// live buffer and the persisted history together, newest first.
func (c *commandContext) runSearch() []string {
	now := c.req.Now
	if now.IsZero() {
		now = time.Now()
	}
	term, scoped, rejected := c.searchTarget()
	if rejected != nil {
		return rejected
	}

	rooms := c.searchRooms(scoped)
	matches := c.searchMatches(rooms, term)
	if len(matches) == 0 {
		if scoped != "" {
			return []string{fmt.Sprintf("no messages match %v in #%v", term, scoped)}
		}
		return []string{fmt.Sprintf("no messages match %v in the rooms I have joined", term)}
	}
	return searchLines(term, matches, now, c.replyBudget())
}

// searchTarget resolves the search term and the optional room filter. A term is
// unusable when it has no letter or digit left after sanitizing, or when it is
// longer than a term can be; a rejected term is never echoed, because the text is
// attacker-chosen.
func (c *commandContext) searchTarget() (term, room string, rejected []string) {
	fields := strings.Fields(c.Args)
	if len(fields) == 0 {
		return "", "", []string{"Usage: " + searchUsage}
	}
	// With more than one token, the last token is a room filter when it names a
	// joined room or is written as "#room"; otherwise the whole argument is the
	// phrase to search for, so "search what is up" searches for those three words
	// together. A single token is always a term, so a room's own name stays
	// searchable ("search general" searches for the word).
	if last := fields[len(fields)-1]; len(fields) > 1 && (strings.HasPrefix(last, "#") || c.joinedRoom(last)) {
		name, ok := c.joinedRoomName(last)
		if !ok {
			return "", "", []string{fmt.Sprintf("no room named %q: I have joined %v",
				safeEcho(strings.TrimPrefix(last, "#"), maxEchoNickBytes), c.joinedRoomSummary())}
		}
		room = name
		fields = fields[:len(fields)-1]
	}
	if len(fields) == 0 {
		return "", "", []string{"Usage: " + searchUsage}
	}
	term = safeEcho(strings.Join(fields, " "), maxSearchTermBytes)
	if !searchTermUsable(term) {
		return "", "", []string{"Usage: " + searchUsage}
	}
	return term, room, nil
}

// searchTermUsable reports whether a sanitized term is worth searching for: it
// must keep a letter or digit and stay within the byte cap. Text that sanitizes
// away entirely is a rejected argument, not an empty search.
func searchTermUsable(term string) bool {
	if term == "" || len(term) > maxSearchTermBytes {
		return false
	}
	for _, r := range term {
		if placeHasSubstance(r) {
			return true
		}
	}
	return false
}

// searchRooms returns the rooms one search reads: the named room, or every room
// the bot has joined, in a stable order and bounded.
func (c *commandContext) searchRooms(scoped string) []string {
	if scoped != "" {
		return []string{scoped}
	}
	seen := make(map[string]bool)
	rooms := make([]string, 0, maxSearchRooms)
	for _, joined := range c.conn().JoinedRoomList() {
		name := normalizeRoom(joined)
		if !persistableRoomName(name) || seen[name] {
			continue
		}
		seen[name] = true
		rooms = append(rooms, name)
	}
	// The room the request arrived in may not be in the joined list yet, and a
	// mention is answered in that room, so it is always searched.
	if name := normalizeRoom(c.req.Room); persistableRoomName(name) && !seen[name] {
		rooms = append(rooms, name)
	}
	sort.Strings(rooms)
	if len(rooms) > maxSearchRooms {
		rooms = rooms[:maxSearchRooms]
	}
	return rooms
}

// searchMatches collects the rows that contain the term, newest first, reading at
// most maxSearchMatches of them. The bot's own replies are skipped: they are
// noise, and one of them is the reply being built.
func (c *commandContext) searchMatches(rooms []string, term string) []*rrc.RRCMessage {
	ownHex := c.reg.identityHex()
	needle := strings.ToLower(term)
	skipID := c.req.Msg.ID
	seen := make(map[string]bool)
	out := make([]*rrc.RRCMessage, 0, 16)
	for _, room := range rooms {
		for _, row := range c.conversationRows(room) {
			if len(out) >= maxSearchMatches {
				break
			}
			if !searchable(row, ownHex) || row.ID != "" && row.ID == skipID {
				continue
			}
			key := row.ID
			if key == "" {
				key = fmt.Sprintf("%v|%v|%v", row.Ts, row.Nick, row.Text)
			}
			if seen[key] {
				continue
			}
			if !strings.Contains(strings.ToLower(row.Text), needle) {
				continue
			}
			seen[key] = true
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ts > out[j].Ts })
	return out
}

// searchable reports whether a row can be reported as a match: it must carry text
// and a clock, must not be a standing pinned greeting, and must not be one of the
// bot's own lines.
func searchable(row *rrc.RRCMessage, ownHex string) bool {
	if row == nil || row.Pinned || row.Ts <= 0 {
		return false
	}
	if strings.TrimSpace(row.Text) == "" {
		return false
	}
	return hexString(row.Src) != ownHex
}

// searchLines renders the search reply: a counted header, the matches that fit
// the reply budget, and an honest trailer when the budget cuts the list short.
func searchLines(term string, matches []*rrc.RRCMessage, now time.Time, budget int) []string {
	rooms := make(map[string]bool)
	for _, row := range matches {
		rooms[normalizeRoom(row.Room)] = true
	}
	lines := []string{fmt.Sprintf("%v for %v in %v (newest first)",
		pluralCount(len(matches), "match", "matches"), term,
		pluralCount(len(rooms), "room", "rooms"))}
	if budget <= 1 {
		return lines
	}
	shown := 0
	for _, row := range matches {
		if len(lines) >= budget-1 || shown == budget-1 {
			break
		}
		lines = append(lines, matchLine(row, now))
		shown++
	}
	if shown < len(matches) {
		lines = append(lines, fmt.Sprintf("… %v more matches not shown", len(matches)-shown))
	}
	return lines
}

// matchLine renders one match: when it happened, where, who said it, and what
// they said, with the peer-supplied parts stripped and shortened.
func matchLine(row *rrc.RRCMessage, now time.Time) string {
	who := safeEcho(normalizeNick(row.Nick), maxEchoNickBytes)
	if who == "" {
		who = shortHash(hexString(row.Src))
	}
	if who == "" {
		who = "unknown"
	}
	text := safeEcho(row.Text, maxSearchTextBytes)
	if text == "" {
		text = "(a message with no readable text)"
	}
	room := safeEcho(normalizeRoom(row.Room), maxEchoNickBytes)
	if room == "" {
		room = "?"
	}
	return fmt.Sprintf("[%v] #%v %v: %v", formatClock(time.UnixMilli(row.Ts), now), room, who, text)
}
