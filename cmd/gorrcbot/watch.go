// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the watch commands: "tell me when this peer or node announces".
// RRC is connection-oriented and a peer only speaks when it is online, but an
// announce is broadcast, so watching for one is the only way to notice a peer the
// bot has never met.
//
// A watch is memory only and deliberately cheap: a filter, an owner, and an
// expiry. It is capped per asker and per bot, it ages out, and it is rate limited
// so a chatty node cannot turn one subscription into a flood of direct notices.
// The notice goes back through the same hub the asker used, because that is the
// only hub that can reach them.

package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

const (
	// watchUsage is the usage line for the watch command.
	watchUsage = "watch <name|hash> [ttl]"
	// unwatchUsage is the usage line for the unwatch command.
	unwatchUsage = "unwatch <n|all>"

	// unwatchAllArg is the one token that means every watch at once.
	unwatchAllArg = "all"
	// watchesUsage is the usage line for the watches command.
	watchesUsage = "watches"
	// minWatchFilterBytes is the shortest usable filter: shorter tokens match far
	// too much to be a subscription.
	minWatchFilterBytes = 2
	// maxWatchFilterBytes bounds one filter.
	maxWatchFilterBytes = 32
	// maxWatchesPerPeer is how many filters one identity may hold.
	maxWatchesPerPeer = 10
	// maxWatchesTotal bounds every filter the bot holds, whatever their owners.
	maxWatchesTotal = 100
	// defaultWatchTTL is how long a watch lasts unless the asker asks for less.
	defaultWatchTTL = 24 * time.Hour
	// maxWatchTTL is the longest a watch can be asked for.
	maxWatchTTL = 7 * 24 * time.Hour
	// watchNoticeInterval is the shortest gap between two notices for one filter.
	watchNoticeInterval = 60 * time.Second
	// minWatchTTL is the shortest watch: shorter than this and the asker would
	// have to watch the clock instead of the announce.
	minWatchTTL = time.Minute
	// watchForgetLine is the promise the confirmation makes, because a watch is
	// not persisted: it must never imply otherwise.
	watchForgetLine = "a bot restart forgets it"
)

// watch is one subscription: who asked, for what, since when, and until when.
type watch struct {
	// ID is the per-owner number shown by the watches command, counted from 1.
	ID int
	// OwnerHex is the identity hash of the asker.
	OwnerHex string
	// OwnerNick is the nick the asker had when they subscribed, for the answer
	// only; matching never uses it.
	OwnerNick string
	// HubHex is the hub the ask was sent through, which is the hub that can
	// reach the asker again.
	HubHex string
	// Filter is the name substring or hash prefix to match.
	Filter string
	// CreatedAt is when the watch was made.
	CreatedAt time.Time
	// ExpiresAt is when it stops matching.
	ExpiresAt time.Time
	// LastNotice is when a notice for this filter was last attempted.
	LastNotice time.Time
}

// watchTable holds every subscription. It is small and bounded, so one mutex over
// a slice is the right shape: no index to keep consistent, and ordering is the
// order the asker sees.
type watchTable struct {
	mu    sync.Mutex
	items []*watch
}

// newWatchTable builds an empty table.
func newWatchTable() *watchTable { return &watchTable{} }

// add records one subscription, enforcing both caps. The returned error is
// user-facing: it says which cap was hit.
func (t *watchTable) add(ownerHex, ownerNick, hubHex, filter string, ttl time.Duration, now time.Time) (*watch, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked(now)
	mine := 0
	for _, w := range t.items {
		if w.OwnerHex == ownerHex {
			mine++
			if strings.EqualFold(w.Filter, filter) {
				return nil, errWatchDuplicate
			}
		}
	}
	if mine >= maxWatchesPerPeer {
		return nil, errWatchLimit
	}
	if len(t.items) >= maxWatchesTotal {
		return nil, errWatchBusy
	}
	w := &watch{
		ID:        mine + 1,
		OwnerHex:  ownerHex,
		OwnerNick: ownerNick,
		HubHex:    hubHex,
		Filter:    filter,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	t.items = append(t.items, w)
	return w, nil
}

// remove drops one subscription by its per-owner number, or every subscription
// the owner holds.
func (t *watchTable) remove(ownerHex, token string, now time.Time) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked(now)
	if strings.EqualFold(strings.TrimSpace(token), unwatchAllArg) {
		kept := t.items[:0]
		removed := 0
		for _, w := range t.items {
			if w.OwnerHex == ownerHex {
				removed++
				continue
			}
			kept = append(kept, w)
		}
		t.items = kept
		t.renumberLocked(ownerHex)
		if removed == 0 {
			return 0, errNoWatches
		}
		return removed, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(token))
	if err != nil || n < 1 {
		return 0, errBadWatchNumber
	}
	kept := t.items[:0]
	removed := 0
	for _, w := range t.items {
		if w.OwnerHex == ownerHex && w.ID == n {
			removed++
			continue
		}
		kept = append(kept, w)
	}
	t.items = kept
	t.renumberLocked(ownerHex)
	if removed == 0 {
		return 0, errNoSuchWatch
	}
	return removed, nil
}

// list returns one owner's subscriptions in the order their numbers imply. It
// returns copies, not the live entries: the command layer reads them on its own
// goroutine, while the drain goroutine is free to update a watch's rate limit.
func (t *watchTable) list(ownerHex string, now time.Time) []watch {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked(now)
	out := make([]watch, 0, maxWatchesPerPeer)
	for _, w := range t.items {
		if w.OwnerHex == ownerHex {
			out = append(out, *w)
		}
	}
	return out
}

// match returns the subscriptions one announce satisfies, dropping the ones whose
// rate limit is not up. It marks them as notified, because the caller has no way
// to report back and one attempt per interval is the promise.
func (t *watchTable) match(a announce) []*watch {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*watch
	for _, w := range t.items {
		if a.At.Before(w.CreatedAt) || !a.At.Before(w.ExpiresAt) {
			continue
		}
		if !announceMatches(a, strings.ToLower(w.Filter)) {
			continue
		}
		if !w.LastNotice.IsZero() && a.At.Sub(w.LastNotice) < watchNoticeInterval {
			continue
		}
		w.LastNotice = a.At
		out = append(out, w)
	}
	return out
}

// expire drops the subscriptions whose time is up and renumbers what remains, so
// an asker's numbers always start at 1.
func (t *watchTable) expire(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked(now)
}

// expireLocked drops expired subscriptions. The caller holds the lock.
func (t *watchTable) expireLocked(now time.Time) {
	kept := t.items[:0]
	dropped := false
	for _, w := range t.items {
		if !now.Before(w.ExpiresAt) {
			dropped = true
			continue
		}
		kept = append(kept, w)
	}
	t.items = kept
	if dropped {
		t.renumberLocked("")
	}
}

// renumberLocked renumbers one owner's subscriptions, or every owner's when
// ownerHex is empty. The caller holds the lock.
func (t *watchTable) renumberLocked(ownerHex string) {
	next := map[string]int{}
	for _, w := range t.items {
		if ownerHex != "" && w.OwnerHex != ownerHex {
			continue
		}
		next[w.OwnerHex]++
		w.ID = next[w.OwnerHex]
	}
}

// watch filter errors, all user-facing.
var (
	// errWatchDuplicate reports a filter the asker already holds.
	errWatchDuplicate = fmt.Errorf("you are already watching for that")
	// errWatchLimit reports the per-identity cap.
	errWatchLimit = fmt.Errorf("you can hold at most %v watches; unwatch one first", maxWatchesPerPeer)
	// errWatchBusy reports the bot-wide cap.
	errWatchBusy = fmt.Errorf("this bot is watching for too many things right now; try again later")
	// errNoWatches reports an unwatch with nothing to unwatch.
	errNoWatches = fmt.Errorf("you are not watching for anything")
	// errBadWatchNumber reports an unwatch token that is not a number. It names
	// both ways out, because a filter name is the natural thing to type here and
	// the answer has to say what would work instead.
	errBadWatchNumber = fmt.Errorf("the watch number must be a number: %v lists them, and %q drops every watch", watchesUsage, unwatchAllArg)
	// errNoSuchWatch reports an unwatch of a number the asker does not hold.
	errNoSuchWatch = fmt.Errorf("you have no watch with that number")
	// errWatchTTL reports an unusable time to live.
	errWatchTTL = fmt.Errorf("the time to watch for must be at least %v and no more than %v, "+
		"as in 30m, 6h or 7d", minWatchTTL, formatTTL(maxWatchTTL))
	// errWatchFilter reports an unusable filter.
	errWatchFilter = fmt.Errorf("a filter needs %v to %v characters: letters, digits, spaces, "+
		"apostrophes, commas, periods, hyphens or underscores", minWatchFilterBytes, maxWatchFilterBytes)
)

// runWatch subscribes the asker to announces matching a name or hash prefix, for
// the default time to live or one they name.
func (c *commandContext) runWatch() []string {
	if c.reg.watches == nil {
		return []string{"watching is unavailable in this build"}
	}
	if strings.TrimSpace(c.Args) == "" {
		return []string{"Usage: " + watchUsage}
	}
	filter, ttl, err := parseWatchRequest(c.Args)
	if err != nil {
		return []string{err.Error()}
	}
	ownerHex := hexString(c.req.Msg.Src)
	w, err := c.reg.watches.add(ownerHex, c.peerName(c.req.Msg.Src), c.session().conn.HubAddressHex(),
		filter, ttl, c.now())
	if err != nil {
		return []string{err.Error()}
	}
	matches, freshest := c.reg.announces.matchingAnnounces(w.Filter)
	line := fmt.Sprintf("watching for %q for %v", w.Filter, formatTTL(w.ExpiresAt.Sub(w.CreatedAt)))
	if matches > 0 {
		// Announces are sparse, so what the cache already holds is worth saying:
		// it is the only thing known about this destination until it announces
		// again, and the age keeps it from reading as "it is announcing now".
		line += "; " + pluralCount(matches, "cached announce matches it", "cached announces match it")
		if !freshest.IsZero() {
			// A clock that went backwards reports "0s ago" rather than an age in
			// the future, which is the only honest reading of an announce that
			// has not happened yet.
			age := max(c.now().Sub(freshest), 0)
			line += ", freshest heard " + countdownMagnitude(age) + " ago"
		}
	}
	lines := []string{line + "; " + watchForgetLine}
	if matches == 0 {
		// A watch that cannot match is worth saying out loud: the alternative is
		// an asker waiting for a notice that the cache they are watching can
		// never produce.
		if hint := watchNoMatchLine(c.reg.announces.size(), w.Filter); hint != "" {
			lines = append(lines, hint)
		}
	}
	// A hub that cannot carry direct notices can never report a match back, so
	// the confirmation says so instead of letting the asker wait for nothing.
	if !c.session().conn.HasCapability(rrc.CapDirectNotice) {
		lines = append(lines, watchDeliveryLine(rrc.ErrDirectNoticesUnsupported))
	}
	return lines
}

// watchNoMatchLine explains a freshly armed watch whose filter the cache does not
// hold, and it is careful about what that means. Sparse announces are exactly why
// this message exists: the cache is only the bot's memory of what it has heard, so
// it will not hold a destination that announced before the bot started, nor one
// whose entry the public mesh has since pushed out. The line therefore promises
// the only thing that is certain — the watch fires when a matching announce
// arrives — instead of implying that the filter can never match.
func watchNoMatchLine(cacheSize int, filter string) string {
	if cacheSize == 0 {
		return "the announce cache is empty, so nothing can match yet: this bot only matches announces it receives itself"
	}
	line := fmt.Sprintf("the cache does not hold it now (%v held, none matching); it fires when a matching announce arrives",
		pluralCount(cacheSize, "announce", "announces"))
	if !isHexString(strings.ToLower(strings.TrimSpace(filter))) {
		// A name is the friendlier filter and the less exact one, and saying so
		// here is useful precisely when the cache cannot help.
		line += ", and a hash prefix is the more exact filter for a destination that does not publish a name"
	}
	return line
}

// parseWatchRequest reads a watch command's arguments: a filter, and optionally
// how long to watch for. A trailing token is a time to live when it parses as one,
// so "retibooks 6h" watches for six hours — and when it does not, the whole
// argument is the filter, because a node's display name may well contain a space.
func parseWatchRequest(args string) (string, time.Duration, error) {
	trimmed := strings.TrimSpace(args)
	fields := strings.Fields(trimmed)
	ttl := defaultWatchTTL
	filterArgs := trimmed
	if len(fields) >= 2 {
		last := fields[len(fields)-1]
		if isDurationToken(last) {
			// A token that is shaped like a duration is one, even when it is too
			// short or too long: that is a request to report, not a name.
			parsed, err := parseWatchTTL(last)
			if err != nil {
				return "", 0, err
			}
			ttl = parsed
			filterArgs = strings.TrimSpace(strings.TrimSuffix(trimmed, last))
		}
	}
	filter, err := sanitizeWatchFilter(filterArgs)
	if err != nil {
		return "", 0, err
	}
	return filter, ttl, nil
}

// isDurationToken reports whether a token is shaped like a duration, which decides
// whether a trailing token is a time to live or part of a name. It deliberately
// accepts a too-short duration, so the answer can say why it is unusable.
func isDurationToken(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if _, err := time.ParseDuration(token); err == nil {
		return true
	}
	digits, found := strings.CutSuffix(token, "d")
	if !found {
		return false
	}
	_, err := strconv.Atoi(digits)
	return err == nil
}

// parseWatchTTL parses a requested time to live. Ordinary Go durations are
// accepted, plus the "d" suffix an asker is likely to type for days. A request
// longer than the maximum is clamped rather than refused, and a nonsense or
// far-too-short one is refused.
func parseWatchTTL(text string) (time.Duration, error) {
	token := strings.ToLower(strings.TrimSpace(text))
	if token == "" {
		return 0, errWatchTTL
	}
	ttl, err := time.ParseDuration(token)
	if err != nil && strings.HasSuffix(token, "d") {
		days, dayErr := strconv.Atoi(strings.TrimSuffix(token, "d"))
		if dayErr == nil {
			ttl, err = time.Duration(days)*24*time.Hour, nil
		}
	}
	if err != nil || ttl < minWatchTTL {
		return 0, errWatchTTL
	}
	return min(ttl, maxWatchTTL), nil
}

// formatTTL renders a time to live the way an asker asked for it: whole hours
// when it is whole hours, otherwise minutes.
func formatTTL(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%vh", int(d.Hours()))
	}
	return fmt.Sprintf("%vm", int(d.Minutes()))
}

// runUnwatch drops one subscription, or all of the asker's.
func (c *commandContext) runUnwatch() []string {
	if c.reg.watches == nil {
		return []string{"watching is unavailable in this build"}
	}
	if strings.TrimSpace(c.Args) == "" {
		return []string{"Usage: " + unwatchUsage}
	}
	removed, err := c.reg.watches.remove(hexString(c.req.Msg.Src), c.Args, c.now())
	if err != nil {
		return []string{err.Error()}
	}
	if strings.EqualFold(strings.TrimSpace(c.Args), unwatchAllArg) {
		return []string{fmt.Sprintf("unwatching %v", pluralCount(removed, "watch", "watches"))}
	}
	return []string{fmt.Sprintf("unwatching %v", c.Args)}
}

// runWatches lists the asker's subscriptions, with how long each has left.
func (c *commandContext) runWatches() []string {
	if c.reg.watches == nil {
		return []string{"watching is unavailable in this build"}
	}
	now := c.now()
	items := c.reg.watches.list(hexString(c.req.Msg.Src), now)
	if len(items) == 0 {
		return []string{"you are not watching for anything; use " + watchUsage}
	}
	lines := make([]string, 0, len(items)+1)
	lines = append(lines, fmt.Sprintf("%v:", pluralCount(len(items), "watch", "watches")))
	for _, w := range items {
		lines = append(lines, fmt.Sprintf("%v. %q, %v left", w.ID, w.Filter, formatAge(w.ExpiresAt.Sub(now))))
	}
	return lines
}

// sanitizeWatchFilter validates a watch filter and returns it trimmed. The rule is
// the same spirit as a place: real letters and digits from any script, plus the
// punctuation a host, node, or peer name plausibly contains — and nothing that
// could restructure a line or hide what it says.
func sanitizeWatchFilter(args string) (string, error) {
	filter := strings.TrimSpace(args)
	// Runs of whitespace collapse to one space, so a mistyped name still matches
	// the announce it was meant for. A leading "@" is the sigil a room member
	// uses to address a peer, never part of the name it announces under, so it
	// is dropped before the filter is validated.
	filter = strings.Join(strings.Fields(filter), " ")
	filter = normalizePeerToken(filter)
	if len(filter) < minWatchFilterBytes || len(filter) > maxWatchFilterBytes {
		return "", errWatchFilter
	}
	substance := false
	for _, r := range filter {
		if !watchFilterAllowed(r) {
			return "", errWatchFilter
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			substance = true
		}
	}
	if !substance {
		return "", errWatchFilter
	}
	return filter, nil
}

// watchFilterAllowed reports whether one rune may appear in a watch filter.
func watchFilterAllowed(r rune) bool {
	if r == '_' {
		return true
	}
	return placeAllowed(r)
}

// watchNoticeLine renders the notice a watcher receives for one announce: what
// announced, how it is addressed, and how far away it is.
func watchNoticeLine(a announce, paths pathLookup) string {
	name := a.Name
	if name == "" {
		name = "(no name)"
	}
	line := fmt.Sprintf("announce: %v %v %v — %v", name, a.Aspect, shortHash(a.DestHex),
		watchPathPhrase(a.DestHex, paths))
	cleaned, err := sanitizeProviderLine(line)
	if err != nil {
		// Every field of a notice is peer-chosen text, so a line that cannot be
		// cleaned is not a line to send.
		return fmt.Sprintf("announce: something announced %v but its details could not be read", a.Aspect)
	}
	return cleaned
}

// watchPathPhrase renders the reachability of an announced destination, which is
// the part of a notice that tells the watcher whether they can act on it yet.
func watchPathPhrase(destHex string, paths pathLookup) string {
	if paths == nil {
		return "reachability unknown"
	}
	destHash, err := hexToBytes(destHex)
	if err != nil {
		return "reachability unknown"
	}
	entry := paths.GetPathEntry(destHash)
	if entry == nil {
		return "no path yet"
	}
	return fmt.Sprintf("%v via %v on %v", pathHops(entry.Hops), pathNextHop(entry.NextHop),
		rns.InterfaceString(entry.Interface))
}

// watchDeliveryLine renders the acknowledgement a watch confirmation adds when the
// asker cannot be reached directly, so nobody is left believing a notice will
// arrive that cannot.
func watchDeliveryLine(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case isError(err, rrc.ErrDirectNoticesUnsupported):
		return "this hub cannot deliver direct notices, so a match cannot be reported to you here"
	case isError(err, rrc.ErrDestinationNotConnected):
		return "you are not connected for direct notices right now"
	default:
		return "the notice could not be sent"
	}
}
