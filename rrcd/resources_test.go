// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"bytes"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

const testResourceTTL = 30.0

// newTestResourceManager builds a ResourceManager with an injected clock
// starting at 100.0 and an adjustable hooks set.
func newTestResourceManager(t *testing.T, mutate func(*ResourceHooks)) (*ResourceManager, *float64) {
	t.Helper()
	now := 100.0
	hooks := ResourceHooks{
		Logf:                           func(string, ...any) {},
		FmtLinkID:                      func(*rns.Link) string { return "00" },
		StatsInc:                       func(string, int) {},
		EnableResourceTransfer:         func() bool { return true },
		MaxResourceBytes:               func() int { return 256 * 1024 },
		MaxPendingResourceExpectations: func() int { return 8 },
		ResourceExpectationTTLs:        func() float64 { return testResourceTTL },
		HasSession:                     func(*rns.Link) bool { return true },
		GetSessionPeer:                 func(*rns.Link) []byte { return nil },
		GetRoomMembers:                 func(string) map[*rns.Link]bool { return nil },
		SendPacket:                     func(*rns.Link, []byte) error { return nil },
		IdentityHash:                   func() []byte { return bytes.Repeat([]byte{0xAB}, 32) },
		SendResource:                   func([]byte, *rns.Link) (ResourceHandle, error) { return nil, nil },
	}
	hooks.Now = func() float64 { return now }
	if mutate != nil {
		mutate(&hooks)
	}
	return NewResourceManager(hooks), &now
}

func TestAddResourceExpectation(t *testing.T) {
	t.Parallel()
	m, now := newTestResourceManager(t, nil)
	link := &rns.Link{}
	rid := []byte{0x01, 0x02}

	if !m.AddResourceExpectation(link, rid, ResKindNotice, 10, nil, "", nil) {
		t.Fatal("AddResourceExpectation() = false, want true")
	}

	key := string(rid)
	set := m.expectations[link]
	if set == nil || len(set.byID) != 1 {
		t.Fatalf("expectations for link = %+v, want one entry", set)
	}
	exp := set.byID[key]
	if exp == nil {
		t.Fatal("expectation not stored under its rid")
	}
	if !bytes.Equal(exp.ID, rid) {
		t.Errorf("exp.ID = %v, want %v", exp.ID, rid)
	}
	if exp.Kind != ResKindNotice {
		t.Errorf("exp.Kind = %q, want %q", exp.Kind, ResKindNotice)
	}
	if exp.Size != 10 {
		t.Errorf("exp.Size = %v, want 10", exp.Size)
	}
	if exp.CreatedAt != 100.0 {
		t.Errorf("exp.CreatedAt = %v, want 100.0", exp.CreatedAt)
	}
	if exp.ExpiresAt != 100.0+testResourceTTL {
		t.Errorf("exp.ExpiresAt = %v, want %v", exp.ExpiresAt, 100.0+testResourceTTL)
	}
	if exp.Room != nil {
		t.Errorf("exp.Room = %v, want nil", roomDeref(exp.Room))
	}
	if exp.SHA256 != nil {
		t.Errorf("exp.SHA256 = %v, want nil", exp.SHA256)
	}
	if exp.Encoding != "" {
		t.Errorf("exp.Encoding = %q, want empty", exp.Encoding)
	}
	if *now != 100.0 {
		t.Errorf("clock advanced to %v, want 100.0", *now)
	}
}

func TestAddResourceExpectationFields(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	link := &rns.Link{}
	rid := []byte{0x03}
	sha := bytes.Repeat([]byte{0xEE}, 32)
	room := "general"

	if !m.AddResourceExpectation(link, rid, ResKindBlob, 42, sha, "utf-8", &room) {
		t.Fatal("AddResourceExpectation() = false, want true")
	}
	exp := m.expectations[link].byID[string(rid)]
	if exp == nil {
		t.Fatal("expectation missing")
	}
	if !bytes.Equal(exp.SHA256, sha) {
		t.Errorf("exp.SHA256 = %v, want %v", exp.SHA256, sha)
	}
	if exp.Encoding != "utf-8" {
		t.Errorf("exp.Encoding = %q, want %q", exp.Encoding, "utf-8")
	}
	if exp.Room == nil || *exp.Room != room {
		t.Errorf("exp.Room = %v, want %q", exp.Room, room)
	}
	if exp.Kind != ResKindBlob {
		t.Errorf("exp.Kind = %q, want %q", exp.Kind, ResKindBlob)
	}
}

func TestAddResourceExpectationCap(t *testing.T) {
	t.Parallel()
	m, now := newTestResourceManager(t, func(h *ResourceHooks) {
		h.MaxPendingResourceExpectations = func() int { return 2 }
	})
	link := &rns.Link{}

	if !m.AddResourceExpectation(link, []byte{1}, ResKindNotice, 10, nil, "", nil) {
		t.Fatal("first add = false, want true")
	}
	if !m.AddResourceExpectation(link, []byte{2}, ResKindNotice, 20, nil, "", nil) {
		t.Fatal("second add = false, want true")
	}
	if m.AddResourceExpectation(link, []byte{3}, ResKindNotice, 30, nil, "", nil) {
		t.Fatal("third add = true, want false (cap reached)")
	}
	if len(m.expectations[link].byID) != 2 {
		t.Fatalf("expectation count = %v, want 2", len(m.expectations[link].byID))
	}

	// Lazy expiry frees cap slots on the next add once the TTL passes.
	*now = 131.0
	if !m.AddResourceExpectation(link, []byte{3}, ResKindNotice, 30, nil, "", nil) {
		t.Fatal("add after lazy expiry = false, want true")
	}
	if len(m.expectations[link].byID) != 1 {
		t.Fatalf("expectation count after expiry = %v, want 1", len(m.expectations[link].byID))
	}
	if _, ok := m.expectations[link].byID[string([]byte{3})]; !ok {
		t.Fatal("new expectation missing after lazy expiry")
	}
}

func TestCleanupExpiredExpectationsBoundary(t *testing.T) {
	t.Parallel()
	m, now := newTestResourceManager(t, nil)
	link := &rns.Link{}
	rid := []byte{1}

	if !m.AddResourceExpectation(link, rid, ResKindNotice, 10, nil, "", nil) {
		t.Fatal("add = false, want true")
	}
	// An expectation expires when expires_at <= now (inclusive boundary).
	*now = 100.0 + testResourceTTL
	m.CleanupExpiredExpectations(link)
	if len(m.expectations[link].byID) != 0 {
		t.Fatalf("expectation count = %v, want 0 after boundary expiry", len(m.expectations[link].byID))
	}
}

func TestCleanupExpiredExpectationsKeepsLive(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	link := &rns.Link{}
	ridLive, ridExpired := []byte{1}, []byte{2}

	m.AddResourceExpectation(link, ridLive, ResKindNotice, 10, nil, "", nil)
	m.AddResourceExpectation(link, ridExpired, ResKindNotice, 20, nil, "", nil)
	set := m.expectations[link]
	set.byID[string(ridExpired)].ExpiresAt = 90.0

	m.CleanupExpiredExpectations(link)

	if _, ok := set.byID[string(ridLive)]; !ok {
		t.Error("live expectation was removed")
	}
	if _, ok := set.byID[string(ridExpired)]; ok {
		t.Error("expired expectation was kept")
	}
	if len(set.order) != 1 || set.order[0] != string(ridLive) {
		t.Errorf("order = %v, want [%v]", set.order, string(ridLive))
	}
}

func TestCleanupAllExpiredExpectations(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	linkA, linkB := &rns.Link{}, &rns.Link{}

	m.AddResourceExpectation(linkA, []byte{1}, ResKindNotice, 10, nil, "", nil)
	m.AddResourceExpectation(linkA, []byte{2}, ResKindNotice, 20, nil, "", nil)
	m.AddResourceExpectation(linkB, []byte{3}, ResKindNotice, 30, nil, "", nil)
	m.expectations[linkA].byID[string([]byte{1})].ExpiresAt = 50.0
	m.expectations[linkB].byID[string([]byte{3})].ExpiresAt = 50.0

	m.CleanupAllExpiredExpectations()

	if len(m.expectations[linkA].byID) != 1 {
		t.Fatalf("linkA expectation count = %v, want 1", len(m.expectations[linkA].byID))
	}
	if _, ok := m.expectations[linkA].byID[string([]byte{2})]; !ok {
		t.Error("linkA live expectation was removed")
	}
	if len(m.expectations[linkB].byID) != 0 {
		t.Errorf("linkB expectation count = %v, want 0", len(m.expectations[linkB].byID))
	}
	// An all-expired link keeps its (now empty) entry, matching the Python
	// dict cleanup that only pops individual rids.
	if _, ok := m.expectations[linkB]; !ok {
		t.Error("linkB expectations entry was removed, want kept-but-empty")
	}
}

func TestPopResourceExpectation(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	link := &rns.Link{}
	rid := []byte{7}

	m.AddResourceExpectation(link, rid, ResKindNotice, 10, nil, "", nil)
	exp := m.PopResourceExpectation(link, rid)
	if exp == nil {
		t.Fatal("PopResourceExpectation() = nil, want the expectation")
	}
	if !bytes.Equal(exp.ID, rid) {
		t.Errorf("popped exp.ID = %v, want %v", exp.ID, rid)
	}
	if m.PopResourceExpectation(link, rid) != nil {
		t.Error("second pop returned an expectation, want nil")
	}
	if len(m.expectations[link].byID) != 0 {
		t.Errorf("expectation count after pop = %v, want 0", len(m.expectations[link].byID))
	}
	if len(m.expectations[link].order) != 0 {
		t.Errorf("order after pop = %v, want empty", m.expectations[link].order)
	}
}

func TestResourceLinkLifecycle(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	link := &rns.Link{}
	rid := []byte{1}

	// Add works on a link that was never established (Python setdefault).
	if !m.AddResourceExpectation(link, rid, ResKindNotice, 10, nil, "", nil) {
		t.Fatal("add on unestablished link = false, want true")
	}

	link2 := &rns.Link{}
	m.OnLinkEstablished(link2)
	if m.expectations[link2] == nil || len(m.expectations[link2].byID) != 0 {
		t.Fatal("OnLinkEstablished did not initialize an empty expectation set")
	}

	m.OnLinkClosed(link)
	if _, ok := m.expectations[link]; ok {
		t.Error("OnLinkClosed left expectations behind")
	}
	// Closing an unknown link is a no-op.
	m.OnLinkClosed(&rns.Link{})

	m.OnLinkClosed(link2)
	m.ClearAll()
	if len(m.expectations) != 0 {
		t.Errorf("expectations after ClearAll = %v, want empty", len(m.expectations))
	}
}

func TestResourceExpectationOrderPreserved(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	link := &rns.Link{}

	m.AddResourceExpectation(link, []byte{1}, ResKindNotice, 10, nil, "", nil)
	m.AddResourceExpectation(link, []byte{2}, ResKindNotice, 10, nil, "", nil)
	m.AddResourceExpectation(link, []byte{3}, ResKindNotice, 10, nil, "", nil)
	// Re-adding an existing rid updates in place without moving position.
	m.AddResourceExpectation(link, []byte{2}, ResKindNotice, 11, nil, "", nil)

	want := []string{string([]byte{1}), string([]byte{2}), string([]byte{3})}
	set := m.expectations[link]
	if len(set.order) != len(want) {
		t.Fatalf("order = %v, want %v", set.order, want)
	}
	for i, key := range want {
		if set.order[i] != key {
			t.Errorf("order[%v] = %v, want %v", i, set.order[i], key)
		}
	}
	if set.byID[string([]byte{2})].Size != 11 {
		t.Error("re-add did not update the expectation in place")
	}
}

func TestMatchResourceExpectationBoundRidWins(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	link := &rns.Link{}
	ridA, ridB := []byte{1}, []byte{2}

	m.AddResourceExpectation(link, ridA, ResKindNotice, 10, nil, "", nil)
	m.AddResourceExpectation(link, ridB, ResKindBlob, 20, nil, "", nil)

	// The bound rid wins even when the size disagrees.
	exp := m.MatchResourceExpectation(link, ridB, 10, nil)
	if exp == nil {
		t.Fatal("MatchResourceExpectation() = nil, want the ridB expectation")
	}
	if !bytes.Equal(exp.ID, ridB) {
		t.Errorf("matched exp.ID = %v, want %v", exp.ID, ridB)
	}
	if exp.Kind != ResKindBlob {
		t.Errorf("matched exp.Kind = %q, want %q", exp.Kind, ResKindBlob)
	}
}

func TestMatchResourceExpectationSizeFallback(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	link := &rns.Link{}
	ridA, ridB, ridC := []byte{1}, []byte{2}, []byte{3}

	// Three same-size expectations; the first inserted wins the fallback.
	m.AddResourceExpectation(link, ridA, ResKindNotice, 10, nil, "", nil)
	m.AddResourceExpectation(link, ridB, ResKindBlob, 10, nil, "", nil)
	m.AddResourceExpectation(link, ridC, ResKindNotice, 99, nil, "", nil)

	exp := m.MatchResourceExpectation(link, nil, 10, nil)
	if exp == nil {
		t.Fatal("MatchResourceExpectation() = nil, want the first size match")
	}
	if !bytes.Equal(exp.ID, ridA) {
		t.Errorf("matched exp.ID = %v, want %v (first inserted)", exp.ID, ridA)
	}

	// Unknown rid falls through to the size scan.
	exp = m.MatchResourceExpectation(link, []byte{9}, 10, nil)
	if exp == nil || !bytes.Equal(exp.ID, ridA) {
		t.Errorf("unknown rid match = %+v, want ridA", exp)
	}

	// No size match at all.
	if exp := m.MatchResourceExpectation(link, nil, 12345, nil); exp != nil {
		t.Errorf("no-match lookup = %+v, want nil", exp)
	}
}

func TestMatchResourceExpectationSHA(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	link := &rns.Link{}
	ridX, ridY := []byte{1}, []byte{2}
	shaX := bytes.Repeat([]byte{0x11}, 32)
	shaY := bytes.Repeat([]byte{0x22}, 32)

	m.AddResourceExpectation(link, ridX, ResKindNotice, 10, shaX, "", nil)
	m.AddResourceExpectation(link, ridY, ResKindNotice, 10, shaY, "", nil)

	// A size match whose sha differs is skipped; the later sha match wins.
	exp := m.MatchResourceExpectation(link, nil, 10, shaY)
	if exp == nil || !bytes.Equal(exp.ID, ridY) {
		t.Errorf("sha-differing match = %+v, want ridY", exp)
	}

	// Only a conflicting sha present.
	if exp := m.MatchResourceExpectation(link, []byte{9}, 10, shaY); exp == nil || !bytes.Equal(exp.ID, ridY) {
		t.Errorf("rid-miss sha match = %+v, want ridY", exp)
	}
}

func TestMatchResourceExpectationSHAlessMatchesAny(t *testing.T) {
	t.Parallel()
	m, _ := newTestResourceManager(t, nil)
	link := &rns.Link{}
	rid := []byte{1}
	shaX := bytes.Repeat([]byte{0x11}, 32)

	// A sha-less expectation matches any same-size actual hash.
	m.AddResourceExpectation(link, rid, ResKindNotice, 10, nil, "", nil)
	exp := m.MatchResourceExpectation(link, nil, 10, shaX)
	if exp == nil || !bytes.Equal(exp.ID, rid) {
		t.Errorf("sha-less match = %+v, want the expectation", exp)
	}

	// A sha'd expectation with no actual sha also matches.
	link2 := &rns.Link{}
	m.AddResourceExpectation(link2, rid, ResKindNotice, 10, shaX, "", nil)
	exp = m.MatchResourceExpectation(link2, nil, 10, nil)
	if exp == nil || !bytes.Equal(exp.ID, rid) {
		t.Errorf("nil-actual-sha match = %+v, want the expectation", exp)
	}
}

func TestMatchResourceExpectationCleansExpired(t *testing.T) {
	t.Parallel()
	m, now := newTestResourceManager(t, nil)
	link := &rns.Link{}

	m.AddResourceExpectation(link, []byte{1}, ResKindNotice, 10, nil, "", nil)
	*now = 100.0 + testResourceTTL
	if exp := m.MatchResourceExpectation(link, nil, 10, nil); exp != nil {
		t.Errorf("expired match = %+v, want nil", exp)
	}
}
