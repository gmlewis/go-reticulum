// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// This file holds the announce cache: the one place in the bot that listens to
// Reticulum announces and remembers what it heard. It exists because announces
// are the only way a peer's display name, identity hash, and aspect become known,
// and because the watch command needs to be told the moment something announces.
//
// Two rules shape it. First, the transport calls the handler on an interface
// read-loop goroutine: the callback must never block, never take a lock that a
// command could hold, and never panic — so it copies what it needs and hands the
// event to a bounded queue, counting anything it has to drop. Second, the table
// is bounded and aged out, because a busy network announces constantly and this
// bot must not grow without limit.

package bot

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gmlewis/go-reticulum/rns"
)

const (
	// announceFilters are the announce aspects this bot listens to. These are the
	// real, hyphenless RNS destination names: a filter with a wrong separator
	// silently matches nothing, which is why they are named here once.
	announceFilters = "lxmf.delivery lxmf.propagation nomadnetwork.node rrc.hub"
	// maxAnnounceEntries bounds the cache table. It has to hold more than a busy
	// mesh sends in one announce cycle of the fleet's own nodes: those announce
	// every six hours by design, and at 512 entries the public mesh evicted the
	// fleet's own announces within about forty minutes, so a name lookup or an
	// arm-time report could not see them at all. 4096 entries of a few hundred
	// bytes each is a megabyte or so, which is nothing next to losing the only
	// memory the bot has of a sparse announcement.
	maxAnnounceEntries = 4096
	// announceTTL is how long one announce stays usable for lookups.
	announceTTL = 24 * time.Hour
	// announceQueueDepth bounds the hand-off queue between the read-loop
	// goroutine and the drain goroutine.
	announceQueueDepth = 256
	// maxAnnounceNameBytes bounds the display name kept from an announce, which
	// is text somebody else chose.
	maxAnnounceNameBytes = 48
	// announcePruneInterval is how often the drain loop ages out the table.
	announcePruneInterval = time.Minute
	// maxAnnounceHashPrefix is the longest hash prefix a lookup token may be.
	maxAnnounceHashPrefix = 64
)

// announce is one announce reduced to the plain fields the bot keeps: no
// transport objects, no identities, no appData blobs.
type announce struct {
	// DestHex is the announced destination's hash, hex.
	DestHex string
	// IdentityHex is the announcing identity's hash, hex, empty when the
	// transport could not report one.
	IdentityHex string
	// Name is the announced display name, sanitized and shortened, empty when
	// the announce carried no usable name.
	Name string
	// Aspect is the filter this announce matched, e.g. "nomadnetwork.node".
	Aspect string
	// At is when the announce was heard.
	At time.Time
}

// announceFeed is the slice of the transport the cache registers handlers on.
// There is no unregister API, so the bot registers once and gates inside the
// callback.
type announceFeed interface {
	RegisterAnnounceHandler(handler *rns.AnnounceHandler)
}

// liveAnnounceFeed registers handlers on a running transport.
type liveAnnounceFeed struct{ ts *rns.TransportSystem }

// RegisterAnnounceHandler implements announceFeed.
func (l liveAnnounceFeed) RegisterAnnounceHandler(handler *rns.AnnounceHandler) {
	l.ts.RegisterAnnounceHandler(handler)
}

// announceCache is the bounded, aged-out announce table plus the queue the
// transport's callback hands events to.
type announceCache struct {
	// feed registers the handlers; nil means no transport, which leaves the
	// cache inert rather than broken.
	feed announceFeed
	// paths answers "how far away is that destination" for a delivery line.
	paths pathLookup
	// watches receives every announce that the cache accepts.
	watches *watchTable
	// deliver sends one watch notice; nil means notices are only logged.
	deliver func(w *watch, a announce)
	// now is the clock, so tests can move time without waiting.
	now func() time.Time

	queue chan announce
	drops atomic.Int64

	mu      sync.Mutex
	entries map[string]announce
	order   []string
}

// newAnnounceCache builds an inert cache. Nothing is registered and no goroutine
// runs until start is called.
func newAnnounceCache() *announceCache {
	return &announceCache{
		now:     time.Now,
		queue:   make(chan announce, announceQueueDepth),
		entries: make(map[string]announce, maxAnnounceEntries),
		watches: newWatchTable(),
	}
}

// start registers one handler per real announce aspect and runs the drain loop
// until stop closes. It is called once, after the transport exists and before the
// hubs connect, so no announce is missed. deliver is the sink for watch notices;
// it may be nil, which leaves notices unwatched but the table populated.
func (c *announceCache) start(feed announceFeed, paths pathLookup, deliver func(*watch, announce),
	wg *sync.WaitGroup, stop <-chan struct{}) {
	if c == nil {
		return
	}
	c.feed = feed
	c.paths = paths
	c.deliver = deliver
	if feed != nil {
		for aspect := range strings.FieldsSeq(announceFilters) {
			feed.RegisterAnnounceHandler(c.handler(aspect))
		}
	}
	if wg != nil && stop != nil {
		wg.Go(func() { c.drain(stop) })
	}
}

// handler builds the transport callback for one aspect. It runs on the interface
// read-loop goroutine, so it does nothing but copy the fields it needs and offer
// the result to a bounded queue: a full queue drops the announce and counts it,
// because blocking here would stall the interface.
func (c *announceCache) handler(aspect string) *rns.AnnounceHandler {
	return &rns.AnnounceHandler{
		AspectFilter: aspect,
		ReceivedAnnounce: func(destHash []byte, identity *rns.Identity, appData []byte) {
			c.offer(aspect, destHash, identityHashOf(identity), appData, c.now())
		},
	}
}

// offer hands one announce to the drain goroutine, or drops it. It never blocks
// and never panics, whatever the transport hands it.
func (c *announceCache) offer(aspect string, destHash, identityHash, appData []byte, at time.Time) {
	if c == nil {
		return
	}
	event := announce{
		DestHex:     hexString(destHash),
		IdentityHex: hexString(identityHash),
		Name:        announceName(appData),
		Aspect:      aspect,
		At:          at,
	}
	if event.DestHex == "" {
		// An announce with no destination hash cannot be looked up or matched
		// against a hash prefix, and storing it would only evict a real entry.
		return
	}
	select {
	case c.queue <- event:
	default:
		c.drops.Add(1)
	}
}

// drain consumes queued announces until stop closes, aging out the table as it
// goes. The event it wakes on is processed here, not handed to pump: a receive
// that only used the wake-up would throw the announce away.
func (c *announceCache) drain(stop <-chan struct{}) {
	ticker := time.NewTicker(announcePruneInterval)
	defer ticker.Stop()
	for {
		select {
		case event := <-c.queue:
			c.process(event)
			c.pump()
		case <-ticker.C:
			c.pump()
			c.prune()
			// Watches expire on the same tick, so a bot nobody asks never
			// accumulates subscriptions that can no longer match.
			if c.watches != nil {
				c.watches.expire(c.now())
			}
		case <-stop:
			return
		}
	}
}

// pump processes every queued announce and returns how many it processed. It is
// the unit the drain loop runs and the unit tests drive directly, so a test never
// has to race a goroutine.
func (c *announceCache) pump() int {
	processed := 0
	for {
		select {
		case event := <-c.queue:
			c.process(event)
			processed++
		default:
			return processed
		}
	}
}

// process stores one announce and fans it out to the watchers. It is the only
// place the table changes, so the ordering between "what is remembered" and "who
// is told" is one function's business.
func (c *announceCache) process(event announce) {
	if event.At.IsZero() {
		event.At = c.now()
	}
	c.store(event)
	if c.watches == nil {
		return
	}
	for _, w := range c.watches.match(event) {
		if c.deliver != nil {
			c.deliver(w, event)
		}
	}
}

// matchingAnnounces reports how many cached announces match a watch filter right
// now, and when the freshest of them was received. The watch confirmation uses it
// because announces are sparse: between two of a destination's announcements the
// cache is the only thing the bot knows about it, so arming a watch has to say
// what is already known rather than only what might arrive.
func (c *announceCache) matchingAnnounces(filter string) (count int, freshest time.Time) {
	if c == nil {
		return 0, time.Time{}
	}
	lower := strings.ToLower(strings.TrimSpace(filter))
	if lower == "" {
		return 0, time.Time{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		if !announceMatches(entry, lower) {
			continue
		}
		count++
		if entry.At.After(freshest) {
			freshest = entry.At
		}
	}
	return count, freshest
}

// store records one announce, evicting the oldest entry when the table is full.
func (c *announceCache) store(event announce) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, seen := c.entries[event.DestHex]; !seen {
		for len(c.entries) >= maxAnnounceEntries && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		c.order = append(c.order, event.DestHex)
	}
	c.entries[event.DestHex] = event
}

// prune drops announces older than the TTL.
func (c *announceCache) prune() {
	if c == nil {
		return
	}
	cutoff := c.now().Add(-announceTTL)
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.order[:0]
	for _, key := range c.order {
		if entry, ok := c.entries[key]; ok && entry.At.After(cutoff) {
			kept = append(kept, key)
			continue
		}
		delete(c.entries, key)
	}
	c.order = kept
}

// size reports how many announces the table holds.
func (c *announceCache) size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// dropped reports how many announces were dropped because the queue was full.
func (c *announceCache) dropped() int64 {
	if c == nil {
		return 0
	}
	return c.drops.Load()
}

// lookup finds the newest announce matching a token: a case-insensitive substring
// of the announced name, or a hex prefix of the destination or identity hash. A
// token shorter than the minimum is not a match, so a stray letter cannot claim a
// whole aspect.
func (c *announceCache) lookup(token string) (announce, bool) {
	if c == nil {
		return announce{}, false
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxAnnounceHashPrefix {
		return announce{}, false
	}
	lower := strings.ToLower(token)
	c.mu.Lock()
	defer c.mu.Unlock()
	var best announce
	found := false
	for _, entry := range c.entries {
		if !announceMatches(entry, lower) {
			continue
		}
		if !found || entry.At.After(best.At) {
			best, found = entry, true
		}
	}
	return best, found
}

// announceMatches reports whether one announce matches an already-lowercased token.
func announceMatches(entry announce, lower string) bool {
	if entry.Name != "" && strings.Contains(strings.ToLower(entry.Name), lower) {
		return true
	}
	if len(lower) < minWatchFilterBytes || !isHexString(lower) {
		return false
	}
	return strings.HasPrefix(entry.DestHex, lower) || strings.HasPrefix(entry.IdentityHex, lower)
}

// announceName turns an announce's appData into a display name: text, trimmed,
// stripped of escapes and control characters, and shortened. A peer chooses this
// text, so nothing about it is trusted.
func announceName(appData []byte) string {
	if len(appData) == 0 {
		return ""
	}
	text := appData
	if len(text) > 4*maxAnnounceNameBytes {
		text = text[:4*maxAnnounceNameBytes]
	}
	// An appData that is not text at all (a binary payload, a truncated rune)
	// has no name to report rather than a mojibake one.
	if !utf8.Valid(text) {
		return ""
	}
	return safeEcho(string(text), maxAnnounceNameBytes)
}

// identityHashOf reports an announced identity's hash, tolerating a nil identity,
// which a path-response announce can carry.
func identityHashOf(identity *rns.Identity) []byte {
	if identity == nil {
		return nil
	}
	return identity.Hash
}
