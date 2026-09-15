// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
)

// fakeFeed stands in for the transport's announce registration: it records the
// handlers the cache registered and lets a test fire an announce, which is what
// the interface read-loop goroutine would do.
type fakeFeed struct {
	mu       sync.Mutex
	handlers map[string]*rns.AnnounceHandler
}

// newFakeFeed builds an empty feed.
func newFakeFeed() *fakeFeed {
	return &fakeFeed{handlers: map[string]*rns.AnnounceHandler{}}
}

// RegisterAnnounceHandler implements announceFeed.
func (f *fakeFeed) RegisterAnnounceHandler(handler *rns.AnnounceHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[handler.AspectFilter] = handler
}

// filters lists the aspects the cache asked to hear.
func (f *fakeFeed) filters() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.handlers))
	for aspect := range f.handlers {
		out = append(out, aspect)
	}
	return out
}

// fire delivers one announce to the handler for an aspect, exactly as the
// transport would: on the caller's goroutine, which is the read-loop goroutine in
// production.
func (f *fakeFeed) fire(aspect string, destHash, identityHash []byte, appData string) {
	f.mu.Lock()
	handler := f.handlers[aspect]
	f.mu.Unlock()
	if handler == nil || handler.ReceivedAnnounce == nil {
		return
	}
	var identity *rns.Identity
	if identityHash != nil {
		identity = &rns.Identity{Hash: identityHash}
	}
	handler.ReceivedAnnounce(destHash, identity, []byte(appData))
}

// watchFixture is a bot with a watch table, an announce cache with a stopped
// clock, a fake transport feed, and one reachable asker.
type watchFixture struct {
	reg     *registry
	session *hubSession
	fake    *fakeHub
	feed    *fakeFeed
	paths   *fakePaths
	cache   *announceCache
	clock   time.Time
}

// newWatchFixture wires a bot exactly as Run does, except the drain loop is not
// started: tests drive pump and prune directly, so nothing races a goroutine.
func newWatchFixture(t *testing.T) *watchFixture {
	t.Helper()
	reg, session, fake := commandFixture(t, nil)
	f := &watchFixture{
		reg:     reg,
		session: session,
		fake:    fake,
		feed:    newFakeFeed(),
		paths:   newFakePaths(),
		cache:   session.bot.announces,
		clock:   catchupBase,
	}
	f.cache.now = func() time.Time { return f.clock }
	// newReplySession builds a session without going through addHub, so the bot
	// must be told about it: delivery looks the session up by hub.
	session.bot.sessions = append(session.bot.sessions, session)
	f.fake.setKnownPeer(hexString(peerHashFor(0x11)), testAskerNick)
	f.fake.setCapability(rrc.CapDirectNotice, true)
	session.bot.announces.start(f.feed, f.paths, session.bot.deliverWatchNotice, nil, nil)
	return f
}

// line runs one command line as the asker.
func (f *watchFixture) line(t *testing.T, line string) []string {
	t.Helper()
	return f.run(t, peerHashFor(0x11), testAskerNick, line)
}

// run runs one command line as some peer.
func (f *watchFixture) run(t *testing.T, peer []byte, nick, line string) []string {
	t.Helper()
	f.fake.setKnownPeer(hexString(peer), nick)
	return f.reg.Run(&commandRequest{
		Session: f.session,
		Msg:     addressedMessageFrom("general", "@gorrcbot "+line, peer),
		Room:    "general",
		Command: line,
		Nick:    "gorrcbot",
		Now:     f.clock,
	})
}

// announce fires one announce and processes everything it queued.
func (f *watchFixture) announce(t *testing.T, aspect, name string, dest, identity []byte) {
	t.Helper()
	f.feed.fire(aspect, dest, identity, name)
	if got := f.cache.pump(); got != 1 {
		t.Fatalf("pump processed %v announces, want 1", got)
	}
}

// notices returns the direct notices the fake hub was asked to send.
func (f *watchFixture) notices() []string {
	f.fake.mu.Lock()
	defer f.fake.mu.Unlock()
	return append([]string(nil), f.fake.direct...)
}

// advance moves the stopped clock forward.
func (f *watchFixture) advance(d time.Duration) { f.clock = f.clock.Add(d) }

// TestWatchConfirmsListsAndUnwatches asserts the whole happy path: a watch is
// confirmed with its TTL and the promise that it is not persisted, listed with the
// time it has left, and removed by number.
func TestWatchConfirmsListsAndUnwatches(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	assertWatchAnswer(t, f.line(t, "watch retibooks"),
		`watching for "retibooks" for 24h; a bot restart forgets it`)
	assertLines(t, f.line(t, "watches"), []string{
		"1 watch:",
		`1. "retibooks", 1d left`,
	})
	assertWatchAnswer(t, f.line(t, "watch nomadnet.node"),
		`watching for "nomadnet.node" for 24h; a bot restart forgets it`)
	assertLines(t, f.line(t, "unwatch 1"), []string{"unwatching 1"})
	// The remaining watch is renumbered, so "1" stays the first one shown.
	assertLines(t, f.line(t, "watches"), []string{
		"1 watch:",
		`1. "nomadnet.node", 1d left`,
	})
	assertLines(t, f.line(t, "unwatch all"), []string{"unwatching 1 watch"})
	assertLines(t, f.line(t, "watches"), []string{"you are not watching for anything; use " + watchUsage})
	assertLines(t, f.line(t, "unwatch all"), []string{errNoWatches.Error()})
	assertLines(t, f.line(t, "unwatch 3"), []string{errNoSuchWatch.Error()})
	assertLines(t, f.line(t, "unwatch x"), []string{errBadWatchNumber.Error()})
	assertLines(t, f.line(t, "unwatch"), []string{"Usage: " + unwatchUsage})
}

// TestWatchRejectsUnusableFilters asserts the filter rule: short, long, and
// structurally dangerous tokens are refused, real names in any script are not.
func TestWatchRejectsUnusableFilters(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	for _, args := range []string{"", "a", strings.Repeat("x", maxWatchFilterBytes+1), "reti/books",
		"ev\x1b[31mil", "...", "reti\u200bbooks"} {
		got := f.line(t, "watch "+args)
		if args == "" {
			assertLines(t, got, []string{"Usage: " + watchUsage})
			continue
		}
		assertLines(t, got, []string{errWatchFilter.Error()})
	}
	// A name in another script is a name, not an attack.
	assertWatchAnswer(t, f.line(t, "watch 京都ノード"),
		`watching for "京都ノード" for 24h; a bot restart forgets it`)
	// Runs of whitespace collapse, so a name typed with a stray newline or a
	// double space still matches the announce it was meant for.
	assertWatchAnswer(t, f.line(t, "watch reti\nbooks"),
		`watching for "reti books" for 24h; a bot restart forgets it`)
}

// TestWatchHonoursARequestedTimeToLive asserts an asker can choose how long to
// watch for, that the request is clamped to the maximum, and that nonsense is
// refused.
func TestWatchHonoursARequestedTimeToLive(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		args  string
		lines []string
		after time.Duration
		left  string
	}{
		{
			name:  "hours",
			args:  "retibooks 6h",
			lines: []string{`watching for "retibooks" for 6h; a bot restart forgets it`},
			after: 5 * time.Hour,
			left:  "1h left",
		},
		{
			name:  "minutes",
			args:  "retibooks 90m",
			lines: []string{`watching for "retibooks" for 90m; a bot restart forgets it`},
			after: 89 * time.Minute,
			left:  "1m left",
		},
		{
			name:  "days are clamped to the maximum",
			args:  "retibooks 30d",
			lines: []string{`watching for "retibooks" for 168h; a bot restart forgets it`},
			after: 167 * time.Hour,
			left:  "1h left",
		},
		{
			name:  "too short",
			args:  "retibooks 5s",
			lines: []string{errWatchTTL.Error()},
		},
		{
			name:  "shaped like a duration but too short",
			args:  "retibooks 30s",
			lines: []string{errWatchTTL.Error()},
		},
		{
			name:  "not shaped like a duration, so it is part of the name",
			args:  "retibooks soon",
			lines: []string{`watching for "retibooks soon" for 24h; a bot restart forgets it`},
			after: 23 * time.Hour,
			left:  "1h left",
		},
		{
			name:  "a spaced name is a filter, not a time",
			args:  "Retibooks  Node",
			lines: []string{`watching for "Retibooks Node" for 24h; a bot restart forgets it`},
			after: 23 * time.Hour,
			left:  "1h left",
		},
		{
			name:  "a lone token that looks like a duration is still a filter",
			args:  "6h",
			lines: []string{`watching for "6h" for 24h; a bot restart forgets it`},
			after: 23 * time.Hour,
			left:  "1h left",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newWatchFixture(t)
			// This table is about reading the arguments, so it asserts the first
			// line and lets the cache hint follow: what the hint says is
			// TestWatchReportsWhatTheCacheCanMatch's subject.
			assertWatchAnswer(t, f.line(t, "watch "+tt.args), tt.lines[0])
			if tt.after == 0 {
				return
			}
			f.advance(tt.after)
			want := strings.Fields(tt.args)[0]
			if tt.name == "a spaced name is a filter, not a time" {
				want = "Retibooks Node"
			}
			if tt.name == "not shaped like a duration, so it is part of the name" {
				want = "retibooks soon"
			}
			assertLines(t, f.line(t, "watches"), []string{"1 watch:", fmt.Sprintf("1. %q, %v", want, tt.left)})
		})
	}
}

// TestWatchExpiryUsesTheRequestedTimeToLive asserts a short watch really stops
// matching when it runs out, not only when it is listed.
func TestWatchExpiryUsesTheRequestedTimeToLive(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	dest := mustHex("c388d7a0b1c2d3e4f5061728394a5b6c")
	f.line(t, "watch retibooks 5m")
	f.advance(5*time.Minute + time.Second)
	// The cache's own expiry tick is what drops it in production; ask the table
	// directly here so the test does not wait a minute.
	f.cache.watches.expire(f.cache.now())
	f.announce(t, "nomadnetwork.node", "Retibooks", dest, nil)
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("notices = %v, want none after a short TTL expired", got)
	}
}

// TestWatchCapsAndDuplicates asserts one asker cannot hold more than the per-peer
// cap and cannot hold the same filter twice.
func TestWatchCapsAndDuplicates(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	assertWatchAnswer(t, f.line(t, "watch alpha"),
		`watching for "alpha" for 24h; a bot restart forgets it`)
	assertLines(t, f.line(t, "watch ALPHA"), []string{errWatchDuplicate.Error()})
	for i := 1; i < maxWatchesPerPeer; i++ {
		f.line(t, fmt.Sprintf("watch filter%v", i))
	}
	assertLines(t, f.line(t, "watch onemore"), []string{errWatchLimit.Error()})
	// Another asker is not affected by the first one's cap.
	assertWatchAnswer(t, f.run(t, peerHashFor(0x22), "Other", "watch onemore"),
		`watching for "onemore" for 24h; a bot restart forgets it`)
}

// TestWatchesArePerAsker asserts each asker sees only their own watches, numbered
// from 1.
func TestWatchesArePerAsker(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	f.run(t, peerHashFor(0x22), "Other", "watch theirs")
	f.line(t, "watch mine")

	assertLines(t, f.line(t, "watches"), []string{"1 watch:", `1. "mine", 1d left`})
	assertLines(t, f.run(t, peerHashFor(0x22), "Other", "watches"), []string{
		"1 watch:", `1. "theirs", 1d left`,
	})
	assertLines(t, f.run(t, peerHashFor(0x33), "Third", "watches"), []string{
		"you are not watching for anything; use " + watchUsage,
	})
}

// TestAnnounceMatchesByNameAndHash asserts a match by name substring (case
// insensitive), by destination hash prefix, and by identity hash prefix, and that
// only the hash-shaped tokens ever match a hash.
func TestAnnounceMatchesByNameAndHash(t *testing.T) {
	t.Parallel()

	dest := mustHex("c388d7a0b1c2d3e4f5061728394a5b6c")
	identity := mustHex("ff2fbc00112233445566778899aabbcc")
	notHash := mustHex("99887766554433221100ffeeddccbbaa")

	for _, tt := range []struct {
		name   string
		filter string
		hit    bool
	}{
		{name: "name substring", filter: "retibooks", hit: true},
		{name: "name is case insensitive", filter: "RETIBOOKS", hit: true},
		{name: "part of the name", filter: "tiboo", hit: true},
		{name: "destination hash prefix", filter: "c388d7a0b1c2", hit: true},
		{name: "identity hash prefix", filter: "ff2fbc001122", hit: true},
		{name: "unrelated hash", filter: "998877665544", hit: false},
		{name: "unrelated name", filter: "somethingelse", hit: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newWatchFixture(t)
			// The watch is armed before the announce arrives, so the cache hint
			// belongs here and the delivery below is this table's subject.
			assertWatchAnswer(t, f.line(t, "watch "+tt.filter),
				fmt.Sprintf("watching for %q for 24h; a bot restart forgets it", tt.filter))
			f.announce(t, "nomadnetwork.node", "Retibooks Node", dest, identity)
			notices := f.notices()
			if tt.hit && len(notices) != 1 {
				t.Fatalf("notices = %v, want exactly one", notices)
			}
			if !tt.hit && len(notices) != 0 {
				t.Fatalf("notices = %v, want none for a non-match", notices)
			}
			if tt.hit {
				want := fmt.Sprintf("announce: Retibooks Node nomadnetwork.node %v — no path yet",
					shortHash(hexString(dest)))
				assertLines(t, notices, []string{want})
			}
			_ = notHash
		})
	}
}

// TestWatchNoticeReportsReachability asserts a notice says how far away the
// announced destination is when the transport knows, and says "no path yet" when
// it does not.
func TestWatchNoticeReportsReachability(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	dest := mustHex("c388d7a0b1c2d3e4f5061728394a5b6c")
	f.paths.setPath(dest, &rns.PathInfo{
		Timestamp: catchupBase,
		NextHop:   mustHex("9f3c9f3c9f3c9f3c9f3c9f3c9f3c9f3c"),
		Hops:      2,
	})
	assertWatchAnswer(t, f.line(t, "watch retibooks"),
		`watching for "retibooks" for 24h; a bot restart forgets it`)
	f.announce(t, "nomadnetwork.node", "Retibooks", dest, nil)
	assertLines(t, f.notices(), []string{
		"announce: Retibooks nomadnetwork.node c388d7a0b1c2 — 2 hops via 9f3c9f3c9f3c on None",
	})
}

// TestWatchNoticeIsRateLimited asserts a chatty node cannot turn one subscription
// into a stream: at most one notice per filter per interval.
func TestWatchNoticeIsRateLimited(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	dest := mustHex("c388d7a0b1c2d3e4f5061728394a5b6c")
	f.line(t, "watch retibooks")

	f.announce(t, "nomadnetwork.node", "Retibooks", dest, nil)
	f.advance(30 * time.Second)
	f.announce(t, "nomadnetwork.node", "Retibooks", dest, nil)
	if got := len(f.notices()); got != 1 {
		t.Fatalf("notices = %v, want 1 inside the rate limit", got)
	}
	f.advance(31 * time.Second)
	f.announce(t, "nomadnetwork.node", "Retibooks", dest, nil)
	if got := len(f.notices()); got != 2 {
		t.Fatalf("notices = %v, want 2 once the interval has passed", got)
	}
}

// TestWatchKeepsTheSubscriptionWhenThePeerCannotBeReached asserts the honest
// failure path: an unreachable watcher is logged, the watch stays, and nothing
// claims the notice was delivered.
func TestWatchKeepsTheSubscriptionWhenThePeerCannotBeReached(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	dest := mustHex("c388d7a0b1c2d3e4f5061728394a5b6c")
	f.line(t, "watch retibooks")
	// The hub forgets the peer, which is what an offline asker looks like.
	f.fake.mu.Lock()
	delete(f.fake.knownPeers, hexString(peerHashFor(0x11)))
	f.fake.mu.Unlock()

	f.announce(t, "nomadnetwork.node", "Retibooks", dest, nil)
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("notices = %v, want none when the asker cannot be reached", got)
	}
	assertLines(t, f.line(t, "watches"), []string{"1 watch:", `1. "retibooks", 1d left`})
}

// TestWatchWithoutDirectNoticeSupportSaysSo asserts the confirmation warns when
// the hub cannot carry direct notices, because such a watch can never report a
// match.
func TestWatchWithoutDirectNoticeSupportSaysSo(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	f.fake.setCapability(rrc.CapDirectNotice, false)
	// A cached announce that matches keeps the no-match hint out of the answer, so
	// this test stays about the capability caveat alone.
	f.cache.process(announce{DestHex: "aa11", Name: "Retibooks", Aspect: "nomadnetwork.node", At: f.clock})
	assertLines(t, f.line(t, "watch retibooks"), []string{
		`watching for "retibooks" for 24h; 1 cached announce matches it now; a bot restart forgets it`,
		watchDeliveryLine(rrc.ErrDirectNoticesUnsupported),
	})
}

// TestWatchesExpireAfterTheirTTL asserts a watch is forgotten when its time is up,
// in the listing and in matching.
func TestWatchesExpireAfterTheirTTL(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	dest := mustHex("c388d7a0b1c2d3e4f5061728394a5b6c")
	f.line(t, "watch retibooks")

	f.advance(defaultWatchTTL - time.Minute)
	assertLines(t, f.line(t, "watches"), []string{"1 watch:", `1. "retibooks", 1m left`})

	f.advance(2 * time.Minute)
	assertLines(t, f.line(t, "watches"), []string{"you are not watching for anything; use " + watchUsage})
	f.announce(t, "nomadnetwork.node", "Retibooks", dest, nil)
	if got := f.notices(); len(got) != 0 {
		t.Fatalf("notices = %v, want none after expiry", got)
	}
	// An announce heard before the watch was made never matches it either.
	f.line(t, "watch retibooks")
	f.cache.watches.expire(f.cache.now())
	f.announce(t, "nomadnetwork.node", "Retibooks", dest, nil)
	if got := len(f.notices()); got != 1 {
		t.Fatalf("notices = %v, want 1 for the announce after re-subscribing", got)
	}
}

// TestAnnounceNameIsSanitizedAndBounded asserts a hostile or empty announce name
// is cleaned up, shortened, or replaced, and never breaks a notice.
func TestAnnounceNameIsSanitizedAndBounded(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	dest := mustHex("c388d7a0b1c2d3e4f5061728394a5b6c")
	// The subscription is by hash prefix, so a name that cleans away to nothing
	// still matches: this test is about the name, not the filter.
	f.line(t, "watch "+shortHash(hexString(dest)))

	f.announce(t, "nomadnetwork.node", "Reti\x1b[31mbooks\x07 Node", dest, nil)
	notices := f.notices()
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want one", notices)
	}
	if strings.ContainsAny(notices[0], "\x1b\x07") {
		t.Errorf("notice %q carries a terminal escape", notices[0])
	}
	if !strings.Contains(notices[0], "Retibooks Node") {
		t.Errorf("notice = %q, want the escape-stripped name", notices[0])
	}

	// A name with no usable text at all falls back to a stated placeholder.
	f.advance(time.Minute)
	f.announce(t, "lxmf.delivery", "\x1b[31m", dest, nil)
	notices = f.notices()
	if got := notices[len(notices)-1]; !strings.HasPrefix(got, "announce: (no name) lxmf.delivery ") {
		t.Errorf("notice = %q, want the no-name placeholder", got)
	}

	// A very long name is shortened rather than pasted into the room.
	f.advance(time.Minute)
	f.announce(t, "rrc.hub", strings.Repeat("N", 400), dest, nil)
	notices = f.notices()
	if got := notices[len(notices)-1]; len(got) > 200 || !strings.Contains(got, "…") {
		t.Errorf("notice = %q, want a shortened name with a marker", got)
	}
}

// TestAnnounceNamesComeOnlyFromUTF8 asserts a binary or truncated appData reports
// no name rather than a mojibake one.
func TestAnnounceNamesComeOnlyFromUTF8(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		appData string
		want    string
	}{
		{name: "empty", appData: "", want: ""},
		{name: "text", appData: "Retibooks", want: "Retibooks"},
		{name: "padded text", appData: "Retibooks  ", want: "Retibooks"},
		{name: "invalid utf8", appData: "Reti\xffooks", want: ""},
		{name: "whitespace only", appData: "   ", want: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := announceName([]byte(tt.appData)); got != tt.want {
				t.Errorf("announceName(%q) = %q, want %q", tt.appData, got, tt.want)
			}
		})
	}
}

// TestAnnounceNamesAreShortenedToTheByteBound asserts the name bound is a byte
// bound that never breaks a rune.
func TestAnnounceNamesAreShortenedToTheByteBound(t *testing.T) {
	t.Parallel()

	got := announceName([]byte(strings.Repeat("京", 100)))
	// The bound is on the text; the shortening marker is added on top of it.
	if len(got) > maxAnnounceNameBytes+len("…") {
		t.Errorf("name is %v bytes, over the %v byte bound plus its marker", len(got), maxAnnounceNameBytes)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Errorf("name %q contains a broken rune", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("name %q should be marked as shortened", got)
	}
}

// TestAnnounceCallbackNeverBlocks asserts the interface read-loop contract: a full
// queue drops and counts rather than waiting, and the callback always returns.
func TestAnnounceCallbackNeverBlocks(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	dest := mustHex("c388d7a0b1c2d3e4f5061728394a5b6c")
	// Nothing is pumped, so the queue fills and the rest must be dropped.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range announceQueueDepth * 3 {
			f.feed.fire("lxmf.delivery", dest, nil, "Retibooks")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the announce callback blocked; it must never wait on a lock or a full queue")
	}
	if got := f.cache.dropped(); got == 0 {
		t.Errorf("dropped = 0, want the overflow counted (queue depth %v)", announceQueueDepth)
	}
	if got := f.cache.size(); got != 0 {
		t.Errorf("size = %v before any pump, want 0", got)
	}
}

// TestAnnounceCacheIsBoundedAndAged asserts the table evicts when full and forgets
// entries older than the TTL.
func TestAnnounceCacheIsBoundedAndAged(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	for i := range maxAnnounceEntries + 10 {
		f.cache.process(announce{
			DestHex: fmt.Sprintf("%064x", i),
			Name:    fmt.Sprintf("node-%v", i),
			Aspect:  "nomadnetwork.node",
			At:      f.clock,
		})
	}
	if got := f.cache.size(); got != maxAnnounceEntries {
		t.Errorf("size = %v, want the table capped at %v", got, maxAnnounceEntries)
	}
	// The oldest entries were evicted, the newest are still there.
	if _, ok := f.cache.lookup(fmt.Sprintf("%064x", maxAnnounceEntries+9)); !ok {
		t.Error("the newest announce should still be cached")
	}
	if _, ok := f.cache.lookup(fmt.Sprintf("%064x", 0)); ok {
		t.Error("the oldest announce should have been evicted")
	}
	f.advance(announceTTL + time.Minute)
	f.cache.prune()
	if got := f.cache.size(); got != 0 {
		t.Errorf("size = %v after the TTL, want 0", got)
	}
}

// TestAnnounceLookupPrefersTheNewestMatch asserts a lookup answers with the most
// recent announce when several match, which is what makes a name usable.
func TestAnnounceLookupPrefersTheNewestMatch(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	older := mustHex("11111111111111111111111111111111")
	newer := mustHex("22222222222222222222222222222222")
	f.cache.process(announce{DestHex: hexString(older), Name: "Retibooks", Aspect: "nomadnetwork.node", At: f.clock})
	f.advance(time.Hour)
	f.cache.process(announce{DestHex: hexString(newer), Name: "Retibooks", Aspect: "rrc.hub", At: f.clock})

	got, ok := f.cache.lookup("retibooks")
	if !ok || got.DestHex != hexString(newer) {
		t.Errorf("lookup = %+v, %v, want the newest match", got, ok)
	}
	// A one-character token is below the minimum for a hash prefix, so it matches
	// nothing even if it happens to be the first character of a hash.
	if got, ok := f.cache.lookup("2"); ok {
		t.Errorf("lookup of a single character = %+v, want no match", got)
	}
	if got, ok := f.cache.lookup("222222222222"); !ok || got.DestHex != hexString(newer) {
		t.Errorf("lookup by hash prefix = %+v, %v, want the destination hash to match", got, ok)
	}
}

// TestAnnounceWithNoDestinationIsIgnored asserts a degenerate announce is dropped
// rather than stored as an unusable entry, and that a path-response announce with
// no identity does not panic.
func TestAnnounceWithNoDestinationIsIgnored(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	f.feed.fire("nomadnetwork.node", nil, nil, "Retibooks")
	if got := f.cache.pump(); got != 0 {
		t.Errorf("pump processed %v announces with no destination, want 0", got)
	}
	if got := f.cache.size(); got != 0 {
		t.Errorf("size = %v, want 0", got)
	}
	// A real destination with no identity is still usable by name and hash.
	f.announce(t, "nomadnetwork.node", "Retibooks", mustHex("abcdefabcdefabcdefabcdefabcdefab"), nil)
	if got := f.cache.size(); got != 1 {
		t.Errorf("size = %v, want the announce without an identity cached", got)
	}
	if _, ok := f.cache.lookup("abcdefabcdef"); !ok {
		t.Error("a destination hash prefix should still match")
	}
}

// TestAnnounceCacheRegistersTheRealFilters asserts the cache listens to the real,
// hyphenless RNS aspects and nothing else: a wrong separator matches nothing at
// all, silently.
func TestAnnounceCacheRegistersTheRealFilters(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	got := f.feed.filters()
	want := strings.Fields(announceFilters)
	if len(got) != len(want) {
		t.Fatalf("registered filters = %v, want %v", got, want)
	}
	for _, aspect := range want {
		if !containsString(got, aspect) {
			t.Errorf("filter %q is missing; registered %v", aspect, got)
		}
	}
	if containsString(got, "rrc.chat") || containsString(got, "nomadnet.node") {
		t.Errorf("registered filters %v include a name no transport uses", got)
	}
}

// TestAnnounceDrainLoopDelivers asserts the production wiring end to end: the
// drain goroutine picks an announce off the queue and sends the notice.
func TestAnnounceDrainLoopDelivers(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	dest := mustHex("c388d7a0b1c2d3e4f5061728394a5b6c")
	f.line(t, "watch retibooks")

	stop := make(chan struct{})
	defer close(stop)
	var wg sync.WaitGroup
	f.cache.start(f.feed, f.paths, f.session.bot.deliverWatchNotice, &wg, stop)
	f.feed.fire("nomadnetwork.node", dest, nil, "Retibooks")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.notices()) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("notices = %v, want the drain loop to deliver one", f.notices())
}

// TestWatchCommandsAreRegistered asserts the three commands are in the table, so
// help lists them and the dispatcher can reach them.
func TestWatchCommandsAreRegistered(t *testing.T) {
	t.Parallel()

	f := newWatchFixture(t)
	for name, usage := range map[string]string{
		"watch": watchUsage, "unwatch": unwatchUsage, "watches": watchesUsage,
	} {
		cmd, ok := f.reg.byName[name]
		if !ok {
			t.Fatalf("%v is not registered; names = %v", name, f.reg.names())
		}
		if cmd.usage != usage {
			t.Errorf("%v usage = %q, want %q", name, cmd.usage, usage)
		}
	}
}

// assertWatchAnswer asserts the first line of a watch answer and that every line
// after it is the no-match hint, so a table about one question need not restate
// the other.
func assertWatchAnswer(t *testing.T, got []string, want string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatal("no answer at all")
	}
	if got[0] != want {
		t.Errorf("line 0 = %q, want %q", got[0], want)
	}
	for i, line := range got[1:] {
		if !strings.HasPrefix(line, "no announce has been cached yet") &&
			!strings.HasPrefix(line, "nothing matches it yet") {
			t.Errorf("line %v = %q, want a no-match hint or nothing at all", i+1, line)
		}
	}
}

// TestWatchReportsWhatTheCacheCanMatch asserts the arm-time answer says what the
// announce cache can actually match here. A filter that matches nothing in the
// cache is a filter that may never fire — the fleet's nomadnetwork.node announces
// carry no display name, so a name filter matches nothing — and the asker must
// not be left to wait for a notice the cache cannot produce.
func TestWatchReportsWhatTheCacheCanMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cached     []announce
		args       string
		wantFirst  string
		wantSecond string
	}{
		{
			name:       "an empty cache cannot match anything",
			args:       "retibooks",
			wantFirst:  `watching for "retibooks" for 24h; a bot restart forgets it`,
			wantSecond: "no announce has been cached yet, so nothing can match: this bot only matches announces it receives itself",
		},
		{
			name:       "a name filter matches nothing in a cache without a name",
			cached:     []announce{{DestHex: "aa11", Name: "", Aspect: "nomadnetwork.node"}},
			args:       "retibooks",
			wantFirst:  `watching for "retibooks" for 24h; a bot restart forgets it`,
			wantSecond: "nothing matches it yet (1 announce cached; an announce publishes a name only sometimes, so a hash prefix is the reliable filter)",
		},
		{
			name:       "a hash prefix that matches nothing says when it could",
			cached:     []announce{{DestHex: "bb22", Name: "Node", Aspect: "nomadnetwork.node"}},
			args:       "aa11",
			wantFirst:  `watching for "aa11" for 24h; a bot restart forgets it`,
			wantSecond: "nothing matches it yet (1 announce cached; a hash prefix matches when that destination announces next)",
		},
		{
			name:      "a filter that already matches says so",
			cached:    []announce{{DestHex: "aa11", Name: "Retibooks Node", Aspect: "nomadnetwork.node"}},
			args:      "retibooks",
			wantFirst: `watching for "retibooks" for 24h; 1 cached announce matches it now; a bot restart forgets it`,
		},
		{
			name: "a matching hash prefix counts every destination it names",
			cached: []announce{
				{DestHex: "aa1122", Name: "One", Aspect: "nomadnetwork.node"},
				{DestHex: "aa1133", Name: "Two", Aspect: "lxmf.delivery"},
			},
			args:      "aa11",
			wantFirst: `watching for "aa11" for 24h; 2 cached announces match it now; a bot restart forgets it`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newWatchFixture(t)
			for _, entry := range tt.cached {
				entry.At = f.clock
				f.cache.process(entry)
			}
			lines := f.line(t, "watch "+tt.args)
			if len(lines) == 0 || lines[0] != tt.wantFirst {
				t.Fatalf("line 0 = %q, want %q", firstOrEmpty(lines), tt.wantFirst)
			}
			if tt.wantSecond == "" {
				if len(lines) != 1 {
					t.Errorf("lines = %q, want just the confirmation", lines)
				}
				return
			}
			if len(lines) != 2 || lines[1] != tt.wantSecond {
				t.Errorf("lines = %q, want the confirmation and %q", lines, tt.wantSecond)
			}
		})
	}
}

// firstOrEmpty reports the first line, or an empty string when there is none.
func firstOrEmpty(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}
