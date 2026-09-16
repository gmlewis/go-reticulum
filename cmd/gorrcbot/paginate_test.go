// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestPaginateSlicePagesAndBoundaries asserts the page arithmetic a catalog
// command depends on: 1-indexed pages, exact chunking, and a page past the end
// reported as missing rather than silently clamped.
func TestPaginateSlicePagesAndBoundaries(t *testing.T) {
	t.Parallel()

	items := []int{1, 2, 3, 4, 5, 6, 7}
	tests := []struct {
		name      string
		requested int
		pageSize  int
		wantItems []int
		wantPage  int
		wantPages int
		wantOK    bool
		wantTotal int
	}{
		{name: "first page", requested: 1, pageSize: 3, wantItems: []int{1, 2, 3}, wantPage: 1, wantPages: 3, wantOK: true, wantTotal: 7},
		{name: "middle page", requested: 2, pageSize: 3, wantItems: []int{4, 5, 6}, wantPage: 2, wantPages: 3, wantOK: true, wantTotal: 7},
		{name: "short last page", requested: 3, pageSize: 3, wantItems: []int{7}, wantPage: 3, wantPages: 3, wantOK: true, wantTotal: 7},
		{name: "past the end", requested: 4, pageSize: 3, wantPage: 4, wantPages: 3, wantTotal: 7},
		{name: "zero is the first page", requested: 0, pageSize: 3, wantItems: []int{1, 2, 3}, wantPage: 1, wantPages: 3, wantOK: true, wantTotal: 7},
		{name: "negative is the first page", requested: -5, pageSize: 3, wantItems: []int{1, 2, 3}, wantPage: 1, wantPages: 3, wantOK: true, wantTotal: 7},
		{name: "a page size below one still shows a row", requested: 1, pageSize: 0, wantItems: []int{1}, wantPage: 1, wantPages: 7, wantOK: true, wantTotal: 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := PaginateSlice(items, tt.requested, tt.pageSize)
			if fmt.Sprint(got.Items) != fmt.Sprint(tt.wantItems) {
				t.Errorf("Items = %v, want %v", got.Items, tt.wantItems)
			}
			if got.Page != tt.wantPage || got.TotalPages != tt.wantPages || got.TotalItems != tt.wantTotal {
				t.Errorf("page %v of %v (%v items) = %+v, want page %v of %v (%v items)",
					got.Page, got.TotalPages, got.TotalItems, got, tt.wantPage, tt.wantPages, tt.wantTotal)
			}
			if got.OK != tt.wantOK {
				t.Errorf("OK = %v, want %v", got.OK, tt.wantOK)
			}
		})
	}
}

// TestPaginateSliceOnAnEmptyList asserts an empty catalog reports no pages and
// no rows rather than one empty page.
func TestPaginateSliceOnAnEmptyList(t *testing.T) {
	t.Parallel()

	got := PaginateSlice([]string{}, 1, 4)
	if got.OK || len(got.Items) != 0 || got.TotalItems != 0 || got.TotalPages != 0 {
		t.Errorf("PaginateSlice of nothing = %+v, want no page", got)
	}
	if line := pageMissingLine(got.Page, got.TotalPages); !strings.Contains(line, "not found") {
		t.Errorf("pageMissingLine = %q, want a not-found line", line)
	}
}

// TestDiscoveryPageSizeFollowsTheReplyBudget asserts a page is sized from the
// operator's own max_reply_lines, so a tighter budget cannot produce a page the
// reply policy would truncate.
func TestDiscoveryPageSizeFollowsTheReplyBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		budget int
		want   int
	}{
		{budget: 0, want: 1},
		{budget: 1, want: 1},
		{budget: 3, want: 1},
		{budget: 4, want: 2},
		{budget: 6, want: 4},
		{budget: DefaultMaxReplyLines, want: 4},
		{budget: 100, want: 4},
	}
	for _, tt := range tests {
		if got := DiscoveryPageSize(tt.budget); got != tt.want {
			t.Errorf("DiscoveryPageSize(%v) = %v, want %v", tt.budget, got, tt.want)
		}
	}
}

// TestPagerSessionRecallsAndForgets asserts a saved page is answered exactly
// once, that a requester with nothing saved gets nothing, and that an entry
// shared between identities is never handed to the wrong one.
func TestPagerSessionRecallsAndForgets(t *testing.T) {
	t.Parallel()

	pager := newPagerSession()
	if _, ok := pager.PopNext("nobody"); ok {
		t.Error("an empty pager answered a page")
	}
	pager.Save("", "tide list 2", nil)
	if _, ok := pager.PopNext(""); ok {
		t.Error("the pager remembered a page for an empty requester hash")
	}

	pager.Save("alice", "tide list CA 3", []string{"page three"})
	if _, ok := pager.PopNext("bob"); ok {
		t.Error("bob was handed alice's page")
	}
	next, ok := pager.PopNext("alice")
	if !ok {
		t.Fatal("alice's saved page was not answered")
	}
	if next.Cmd != "tide list CA 3" || len(next.Lines) != 1 || next.Lines[0] != "page three" {
		t.Errorf("PopNext = %+v, want the saved command and lines", next)
	}
	if _, again := pager.PopNext("alice"); again {
		t.Error("a page was answered twice")
	}
}

// TestPagerSessionExpiresAndEvicts asserts the two bounds on the pager table: an
// entry older than the ttl answers like an absent one, and the table never grows
// past its cap.
func TestPagerSessionExpiresAndEvicts(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	pager := newPagerSession()
	pager.maxEntries = 2
	pager.now = func() time.Time { return clock }

	pager.Save("fresh", "tide list 2", nil)
	clock = clock.Add(pagerTTL - time.Second)
	if _, ok := pager.PopNext("fresh"); !ok {
		t.Error("an entry inside the ttl was expired")
	}

	pager.Save("stale", "tide list 2", nil)
	clock = clock.Add(2 * pagerTTL)
	if _, ok := pager.PopNext("stale"); ok {
		t.Error("an entry older than the ttl was answered")
	}

	clock = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	for _, hash := range []string{"one", "two", "three"} {
		pager.Save(hash, "tide list 2", nil)
	}
	if _, ok := pager.PopNext("one"); ok {
		t.Error("the oldest entry was not evicted at the cap")
	}
	if _, ok := pager.PopNext("three"); !ok {
		t.Error("the newest entry was evicted instead of the oldest")
	}
}

// TestMoreAnswersTheNextPageAndChains asserts the whole pager contract through
// the registry: a paged answer tells the asker how to continue, a bare "more"
// continues it, and the walk ends with the expired line rather than a repeat.
func TestMoreAnswersTheNextPageAndChains(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, tideConfig())

	first := runLines(t, reg, session, "tide list CA")
	if len(first) == 0 || !strings.Contains(first[0], "Tide stations in CA (Page 1 of 3):") {
		t.Fatalf("tide list CA = %v, want page 1 of 3 Californian stations", first)
	}
	if want := `[Page 1 of 3: ask "tide list CA 2" or "more" for next]`; first[len(first)-1] != want {
		t.Errorf("footer = %q, want %q", first[len(first)-1], want)
	}

	second := runLines(t, reg, session, "more")
	if len(second) == 0 || !strings.Contains(second[0], "(Page 2 of 3):") {
		t.Fatalf("more = %v, want page 2", second)
	}
	if second[0] == first[0] {
		t.Error("more repeated the page it followed")
	}

	third := runLines(t, reg, session, "next")
	if len(third) == 0 || !strings.Contains(third[0], "(Page 3 of 3):") {
		t.Fatalf("next = %v, want page 3", third)
	}
	if want := "[Page 3 of 3: end of results]"; third[len(third)-1] != want {
		t.Errorf("last footer = %q, want %q", third[len(third)-1], want)
	}

	if lines := runLines(t, reg, session, "more"); len(lines) != 1 || lines[0] != pagerExpiredLine {
		t.Errorf("more past the end = %v, want %q", lines, pagerExpiredLine)
	}
}

// TestMoreWithoutASearchSaysSo asserts the pager never invents a page for an
// asker who has not searched.
func TestMoreWithoutASearchSaysSo(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, tideConfig())
	if lines := runLines(t, reg, session, "more"); len(lines) != 1 || lines[0] != pagerExpiredLine {
		t.Errorf("more with no search = %v, want %q", lines, pagerExpiredLine)
	}
}

// TestPagerIsPerRequester asserts one asker's page is never handed to another
// identity, which is what keeps a shared room from turning a private search into
// somebody else's answer.
func TestPagerIsPerRequester(t *testing.T) {
	t.Parallel()

	reg, session, _ := commandFixture(t, tideConfig())
	alice := peerHashFor(0x11)
	bob := peerHashFor(0x44)
	run := func(src []byte, line string) []string {
		return reg.Run(&commandRequest{
			Session: session,
			Msg:     addressedMessageFrom("general", "@gorrcbot "+line, src),
			Room:    "general",
			Command: line,
			Nick:    "gorrcbot",
			Now:     time.Now(),
		})
	}

	if lines := run(alice, "tide list CA"); len(lines) == 0 {
		t.Fatal("alice's search answered nothing")
	}
	if lines := run(bob, "more"); len(lines) != 1 || lines[0] != pagerExpiredLine {
		t.Errorf("bob's more = %v, want %q", lines, pagerExpiredLine)
	}
	if lines := run(alice, "more"); len(lines) == 0 || !strings.Contains(lines[0], "Page 2 of 3") {
		t.Errorf("alice's more = %v, want page 2 of her own search", lines)
	}
}

// TestMicronLinksRenderTheRowsAndTheNextPage asserts the optional Micron
// notation: the identifier a row names becomes the command that fetches it, and
// the footer offers the next page as a link instead of as a quoted command.
func TestMicronLinksRenderTheRowsAndTheNextPage(t *testing.T) {
	t.Parallel()

	cfg := tideConfig()
	cfg.MicronLinks = true
	reg, session, _ := commandFixture(t, cfg)

	lines := runLines(t, reg, session, "tide list CA")
	if len(lines) < 3 {
		t.Fatalf("tide list CA = %v, want a header, rows, and a footer", lines)
	}
	if !strings.Contains(lines[1], `["9410135":/msg gorrcbot tide 9410135]`) {
		t.Errorf("row = %q, want a Micron link to the station", lines[1])
	}
	if want := `["Next Page":/msg gorrcbot tide list CA 2]`; lines[len(lines)-1] != want {
		t.Errorf("footer = %q, want %q", lines[len(lines)-1], want)
	}
}
