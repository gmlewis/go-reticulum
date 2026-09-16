// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the pagination every catalog command shares: how a long list
// is cut into pages that fit one reply, how a page names itself and the page
// after it, and how a requester's "more" is remembered between messages.
//
// The bot is a low-bandwidth citizen. Every reply line is one envelope on a link
// that may carry a few hundred bits a second, and the reply policy truncates
// anything past max_reply_lines. A catalog of a hundred stations therefore
// cannot be answered in one message: it is answered in pages of a few rows, each
// carrying the header and the exact words that ask for the next one. The page
// size follows the operator's own budget, so raising max_reply_lines widens
// every page without a code change.
//
// A pager entry holds the command that answers the next page rather than the
// page itself. A walk through a hundred rows therefore costs one short string
// per requester, and the page that a "more" produces is rendered by the same
// code that rendered the first one, so its own footer is correct.

package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Pager bounds and wordings.
const (
	// pagerTTL is how long a cached page turn stays answerable. A search is a
	// conversation: the asker reads one page and asks for the next while the
	// question is still in mind, and a search abandoned an hour ago should not
	// answer at all.
	pagerTTL = 5 * time.Minute
	// pagerMaxEntries bounds the pager table, so a busy hub cannot grow the
	// bot's memory without limit. The oldest entry is evicted past this many
	// requesters.
	pagerMaxEntries = 256
	// pagerMoreWord is the shortcut a page footer offers alongside the explicit
	// next-page command.
	pagerMoreWord = "more"
	// pagerExpiredLine is the answer to "more" when nothing is cached for the
	// requester: either they never searched, or the search aged out.
	pagerExpiredLine = "no more pages or search expired"
	// discoveryPageMax is the largest page any catalog command produces. A page
	// is a few rows plus its header and footer, which is what keeps one answer
	// inside a handful of envelopes.
	discoveryPageMax = 4
	// discoveryNearLimit is how many rows a "near" answer names. The three
	// closest stations are the useful answer; a longer list is a lookup, not a
	// decision.
	discoveryNearLimit = 3
	// metersPerNauticalMile converts a distance to the unit a mariner reads.
	metersPerNauticalMile = 1852.0
	// maxDiscoveryEchoBytes bounds the asker's own words when a page repeats
	// them back. A NOTICE is rendered by other people's terminals, so a long or
	// hostile query is shortened and stripped exactly like a provider answer.
	maxDiscoveryEchoBytes = 48
)

// PagedResult is one page of a longer list.
type PagedResult[T any] struct {
	// Items is the page's rows; empty when the page does not exist.
	Items []T
	// Page is the 1-indexed page this result is for, after clamping.
	Page int
	// TotalPages is how many pages the whole list occupies, 0 when it is empty.
	TotalPages int
	// TotalItems is how many rows the whole list holds.
	TotalItems int
	// OK reports that the requested page exists inside the list.
	OK bool
}

// PaginateSlice cuts a list into one page. Pages are 1-indexed, because that is
// how the asker types them. A requested page below 1 is read as page 1; a
// requested page past the end is reported as missing rather than clamped, so the
// answer can name the page the list really ends at. A page size below 1 is read
// as 1, because a page with no rows is a reply with nothing in it.
func PaginateSlice[T any](items []T, requestedPage, pageSize int) PagedResult[T] {
	pageSize = max(pageSize, 1)
	page := max(requestedPage, 1)
	total := len(items)
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	result := PagedResult[T]{Page: page, TotalPages: totalPages, TotalItems: total}
	if total == 0 || page > totalPages {
		return result
	}
	start := (page - 1) * pageSize
	result.Items = items[start:min(start+pageSize, total)]
	result.OK = true
	return result
}

// DiscoveryPageSize returns how many catalog rows one page may carry under the
// operator's reply budget. The header and the footer are two lines and the rest
// is rows, capped because a page long enough to need scrolling would defeat the
// point of paginating in the first place.
func DiscoveryPageSize(maxReplyLines int) int {
	return min(max(maxReplyLines-2, 1), discoveryPageMax)
}

// PagerNext is one cached page turn: the reply to answer with, when it is
// already known, and the command that produces the page after it.
type PagerNext struct {
	// Cmd is the command line that answers the next page, empty when none is
	// cached.
	Cmd string
	// Lines is the reply already rendered for the next page, nil when the page
	// must be produced by running Cmd.
	Lines []string
}

// PagerSession remembers the page each requester is on, so "more" and "next"
// answer the page after the one just sent without the asker repeating the query.
//
// It is deliberately small and in-memory: a page turn is worth remembering for
// as long as the asker is reading the reply and no longer, so nothing is written
// to disk, a restart forgets every pager, and the table is capped and expires.
type PagerSession struct {
	mu         sync.Mutex
	entries    map[string]pagerEntry
	order      []string
	now        func() time.Time
	ttl        time.Duration
	maxEntries int
}

// pagerEntry is one requester's cached page turn and when it was recorded.
type pagerEntry struct {
	next PagerNext
	at   time.Time
}

// newPagerSession builds an empty pager table.
func newPagerSession() *PagerSession {
	return &PagerSession{
		entries:    make(map[string]pagerEntry),
		now:        time.Now,
		ttl:        pagerTTL,
		maxEntries: pagerMaxEntries,
	}
}

// Save records the reply a requester's next "more" answers with, along with the
// command that asks for the page after it. An empty requester hash is refused:
// without an identity there is nobody to remember the page for, and a shared
// slot would hand one asker's page to another.
func (p *PagerSession) Save(requesterHash, nextCmd string, lines []string) {
	hash := strings.TrimSpace(requesterHash)
	if p == nil || hash == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()
	if _, ok := p.entries[hash]; !ok {
		p.order = append(p.order, hash)
	}
	p.entries[hash] = pagerEntry{next: PagerNext{Cmd: nextCmd, Lines: lines}, at: p.now()}
	p.evictLocked()
}

// PopNext returns and forgets the page turn cached for a requester. An entry
// older than the ttl is expired first, so a stale pager answers exactly like an
// absent one.
func (p *PagerSession) PopNext(requesterHash string) (PagerNext, bool) {
	hash := strings.TrimSpace(requesterHash)
	if p == nil || hash == "" {
		return PagerNext{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expireLocked()
	entry, ok := p.entries[hash]
	if !ok {
		return PagerNext{}, false
	}
	delete(p.entries, hash)
	p.removeOrderLocked(hash)
	return entry.next, true
}

// expireLocked drops every entry that has aged out. The caller holds the lock.
func (p *PagerSession) expireLocked() {
	if p.ttl <= 0 {
		return
	}
	cutoff := p.now().Add(-p.ttl)
	kept := p.order[:0]
	for _, hash := range p.order {
		entry, ok := p.entries[hash]
		if !ok {
			continue
		}
		if entry.at.Before(cutoff) {
			delete(p.entries, hash)
			continue
		}
		kept = append(kept, hash)
	}
	p.order = kept
}

// evictLocked drops the oldest entries until the table is back inside its cap.
// The caller holds the lock.
func (p *PagerSession) evictLocked() {
	if p.maxEntries <= 0 {
		return
	}
	for len(p.entries) > p.maxEntries && len(p.order) > 0 {
		hash := p.order[0]
		p.order = p.order[1:]
		delete(p.entries, hash)
	}
}

// removeOrderLocked forgets one hash in the recency order. The caller holds the
// lock.
func (p *PagerSession) removeOrderLocked(hash string) {
	for i, candidate := range p.order {
		if candidate != hash {
			continue
		}
		p.order = append(p.order[:i], p.order[i+1:]...)
		return
	}
}

// discoveryQuery is the request a catalog page echoes back in its header and
// footer, so an asker can read the page and then ask for the next one without
// remembering how they got there.
type discoveryQuery struct {
	// Command is the command word, like "tide".
	Command string
	// Catalog names the rows in the header, like "Tide stations".
	Catalog string
	// Kind is the sub-command that produced the page: search, near, or list.
	Kind string
	// Text is what the kind was given: the search words, the region filter, or
	// the location. Empty when the kind takes none.
	Text string
}

// header renders the page's first line, which names what was asked and where
// the page sits in the answer. The asker's own words are sanitized and bounded
// before they are repeated.
func (q discoveryQuery) header(page, totalPages int) string {
	echo := safeEcho(q.Text, maxDiscoveryEchoBytes)
	switch q.Kind {
	case discoveryKindNear:
		return fmt.Sprintf("%v near %v (Page %v of %v):", q.Catalog, echo, page, totalPages)
	case discoveryKindList:
		if echo != "" {
			return fmt.Sprintf("%v in %v (Page %v of %v):", q.Catalog, echo, page, totalPages)
		}
		return fmt.Sprintf("%v (Page %v of %v):", q.Catalog, page, totalPages)
	default:
		return fmt.Sprintf("%v matching %q (Page %v of %v):", q.Catalog, echo, page, totalPages)
	}
}

// command renders the command line that asks for one page of this query, which
// is what the footer quotes and what a page turn re-runs.
func (q discoveryQuery) command(page int) string {
	parts := make([]string, 0, 4)
	parts = append(parts, q.Command)
	if q.Kind != "" {
		parts = append(parts, q.Kind)
	}
	if strings.TrimSpace(q.Text) != "" {
		parts = append(parts, q.Text)
	}
	if q.Kind != discoveryKindNear {
		parts = append(parts, fmt.Sprint(page))
	}
	return strings.Join(parts, " ")
}

// pageMissingLine is the answer to a page past the end of the list: the page
// that was asked for, and the page the list really ends at, so the asker can go
// there instead of guessing.
func pageMissingLine(page, totalPages int) string {
	if totalPages <= 0 {
		return fmt.Sprintf("page %v not found (no results)", page)
	}
	return fmt.Sprintf("page %v not found (total %v pages)", page, totalPages)
}

// requesterHash is the identity a page turn is remembered for: the hash of the
// peer that asked. It is empty when the request carries no sender, in which case
// nothing is paged and "more" answers that there is nothing to turn to.
func (c *commandContext) requesterHash() string {
	if c.req == nil || c.req.Msg == nil {
		return ""
	}
	return hexString(c.req.Msg.Src)
}

// discoveryPageSize is how many catalog rows this bot may put on one page, read
// from the operator's reply budget.
func (c *commandContext) discoveryPageSize() int {
	cfg := c.reg.config()
	if cfg == nil {
		return discoveryPageMax
	}
	return DiscoveryPageSize(cfg.MaxReplyLines)
}

// micronLinks reports whether this bot renders its discovery rows as clickable
// Micron links, which is what a NomadNet client displays as buttons and every
// other client displays literally. It is off unless the operator turns it on.
func (c *commandContext) micronLinks() bool {
	cfg := c.reg.config()
	return cfg != nil && cfg.MicronLinks
}

// micronLink renders one clickable Micron link: ["label":command].
func micronLink(label, command string) string {
	return fmt.Sprintf("[%q:%v]", label, command)
}

// saveNextPage records the command that answers the page after page, so a bare
// "more" reaches it. Nothing is recorded past the last page, which is what makes
// "more" answer "no more pages" at the end of a walk.
func (c *commandContext) saveNextPage(q discoveryQuery, page, totalPages int) {
	if page >= totalPages {
		return
	}
	c.reg.pager.Save(c.requesterHash(), q.command(page+1), nil)
}

// renderCatalogPage renders one page of a search or list answer, with the header
// and footer that name it and ask for the next page, and remembers the page
// after it.
func (c *commandContext) renderCatalogPage(q discoveryQuery, entries []catalogEntry, page int) []string {
	result := PaginateSlice(entries, page, c.discoveryPageSize())
	if !result.OK {
		return []string{pageMissingLine(result.Page, result.TotalPages)}
	}
	lines := make([]string, 0, len(result.Items)+2)
	lines = append(lines, q.header(result.Page, result.TotalPages))
	for _, entry := range result.Items {
		lines = append(lines, c.catalogRow(q, entry, nil))
	}
	lines = append(lines, c.catalogFooter(q, result.Page, result.TotalPages)...)
	c.saveNextPage(q, result.Page, result.TotalPages)
	return lines
}

// renderNearPage renders the closest rows to a point as the single page a "near"
// answer is.
func (c *commandContext) renderNearPage(q discoveryQuery, near []catalogDistance) []string {
	if len(near) == 0 {
		return []string{fmt.Sprintf("No %v near %v.", q.Catalog, q.Text)}
	}
	lines := make([]string, 0, len(near)+2)
	lines = append(lines, q.header(1, 1))
	for i := range near {
		lines = append(lines, c.catalogRow(q, near[i].Entry, &near[i]))
	}
	lines = append(lines, c.catalogFooter(q, 1, 1)...)
	return lines
}

// catalogRow renders one catalog row: the identity the command takes, and the
// place it is, with the distance and bearing when the row came from a proximity
// search. A bot that names itself as reachable in Micron renders the identity as
// the command that fetches it.
func (c *commandContext) catalogRow(q discoveryQuery, entry catalogEntry, distance *catalogDistance) string {
	id := entry.ID
	if c.micronLinks() {
		id = micronLink(entry.ID, "/msg "+c.echoNick()+" "+q.Command+" "+entry.ID)
	}
	if distance == nil {
		return fmt.Sprintf("  %v: %v", id, entry.Label)
	}
	return fmt.Sprintf("  %v (%.1f nmi %v): %v", id,
		distance.Meters/metersPerNauticalMile, CompassPoint(distance.Bearing), entry.Label)
}

// catalogFooter renders the page's last lines: the exact command that asks for
// the next page, the "more" shortcut, and the end of the list. A page that opens
// with clickable Micron links offers the next one as a link too.
func (c *commandContext) catalogFooter(q discoveryQuery, page, totalPages int) []string {
	if page >= totalPages {
		return []string{fmt.Sprintf("[Page %v of %v: end of results]", page, totalPages)}
	}
	next := q.command(page + 1)
	if c.micronLinks() {
		return []string{micronLink("Next Page", "/msg "+c.echoNick()+" "+safeEcho(next, maxDiscoveryEchoBytes))}
	}
	return []string{fmt.Sprintf("[Page %v of %v: ask %q or %q for next]",
		page, totalPages, next, pagerMoreWord)}
}

// echoNick is the nick this bot answers to, sanitized for use inside a reply it
// signs with its own name. It is what a Micron link addresses, so it must be a
// nick the bot really answers to and short enough not to swamp the envelope.
func (c *commandContext) echoNick() string {
	nick := safeEcho(c.effectiveTriggerNick(), maxEchoNickBytes)
	if nick == "" {
		return DefaultTriggerNick
	}
	return nick
}

// runMore answers "more" and "next" with the page the requester's last search
// left pending. The saved command is re-run rather than the saved page being
// replayed, so the page it produces carries its own footer and the walk can
// continue to the end of the list.
func (c *commandContext) runMore() []string {
	next, ok := c.reg.pager.PopNext(c.requesterHash())
	if !ok {
		return []string{pagerExpiredLine}
	}
	if len(next.Lines) > 0 {
		return next.Lines
	}
	if strings.TrimSpace(next.Cmd) == "" {
		return []string{pagerExpiredLine}
	}
	return c.reg.Run(&commandRequest{
		Session: c.req.Session,
		Msg:     c.req.Msg,
		Room:    c.req.Room,
		Command: next.Cmd,
		Direct:  c.req.Direct,
		Nick:    c.req.Nick,
		Now:     c.now(),
	})
}

// isPagerCommand reports whether an addressed line is one of the page-turn
// shortcuts, which are the only commands the reply policy exempts from the
// per-identity cooldown: a page turn is solicited by the page before it, and it
// can only answer a page the asker already asked for.
func isPagerCommand(line string) bool {
	name, _ := splitCommandLine(line)
	return name == "more" || name == "next"
}
