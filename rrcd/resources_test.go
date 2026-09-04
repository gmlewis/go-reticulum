// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrcd/cbor"
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

	set := m.expectations[link]
	if set == nil || len(set.byID) != 1 {
		t.Fatalf("expectations for link = %+v, want one entry", set)
	}
	exp := set.byID[string(rid)]
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

// fakeResource is a ResourceHandle test double.
type fakeResource struct {
	status int
	data   []byte
}

func (f *fakeResource) Status() int  { return f.status }
func (f *fakeResource) Data() []byte { return f.data }

// recorder captures the sends and stats of a resource-manager test.
type resourceRecorder struct {
	sent      []sentPacket
	stats     map[string]int
	accepted  []*rns.Link
	acceptedK []string
	logs      []string
}

// G10.4 OnResourceConcluded mirrors _resource_concluded: unbinding, the
// status gate, the sha256 match (bound rid wins over size), the mismatch
// keeping the expectation, the pop + stats, and the dispatch call.
func TestOnResourceConcluded(t *testing.T) {
	t.Parallel()

	link := &rns.Link{}
	memberA := &rns.Link{}
	payload := []byte("hello resource")
	sum := sha256Digest(payload)
	rid := []byte{1, 2, 3, 4, 5, 6, 7, 8}

	setup := func(t *testing.T) (*ResourceManager, *resourceRecorder, *float64) {
		t.Helper()
		rec := &resourceRecorder{stats: map[string]int{}}
		m, now := newTestResourceManager(t, func(h *ResourceHooks) {
			h.StatsInc = func(key string, delta int) { rec.stats[key] += delta }
			h.SendPacket = func(l *rns.Link, p []byte) error {
				rec.sent = append(rec.sent, sentPacket{link: l, payload: p})
				return nil
			}
			h.GetSessionPeer = func(*rns.Link) []byte { return bytesOf(0xcc, 32) }
			h.GetRoomMembers = func(string) map[*rns.Link]bool {
				return map[*rns.Link]bool{link: true, memberA: true}
			}
		})
		room := "general"
		if !m.AddResourceExpectation(link, rid, ResKindNotice, len(payload), sum, "", &room) {
			t.Fatal("AddResourceExpectation() = false, want true")
		}
		return m, rec, now
	}

	t.Run("complete matching transfer pops and dispatches", func(t *testing.T) {
		t.Parallel()
		m, rec, _ := setup(t)
		res := &fakeResource{status: rns.ResourceStatusComplete, data: payload}
		m.BindStartedResource(link, res)
		m.OnResourceConcluded(link, res)

		if rec.stats["resources_received"] != 1 || rec.stats["resource_bytes_received"] != len(payload) {
			t.Errorf("stats = %+v, want resources_received=1 bytes=%v", rec.stats, len(payload))
		}
		if exp := m.GetResourceExpectationByRID(link, rid); exp != nil {
			t.Errorf("expectation survived the conclusion: %+v", exp)
		}
		// The dispatch forwarded the NOTICE to the room members except
		// the sender.
		if len(rec.sent) != 1 {
			t.Fatalf("dispatch sent = %+v, want one forwarded envelope", rec.sent)
		}
		if rec.sent[0].link != memberA {
			t.Errorf("dispatch recipient = %v, want memberA", rec.sent[0].link)
		}
	})

	t.Run("failed status leaves everything in place", func(t *testing.T) {
		t.Parallel()
		m, rec, _ := setup(t)
		res := &fakeResource{status: rns.ResourceStatusFailed, data: payload}
		m.BindStartedResource(link, res)
		m.OnResourceConcluded(link, res)

		if rec.stats["resources_received"] != 0 {
			t.Errorf("stats = %+v, want no resources_received", rec.stats)
		}
		if exp := m.GetResourceExpectationByRID(link, rid); exp == nil {
			t.Error("expectation was popped on a failed transfer")
		}
		if len(rec.sent) != 0 {
			t.Errorf("failed transfer sent = %+v, want none", rec.sent)
		}
	})

	t.Run("sha mismatch keeps the expectation", func(t *testing.T) {
		t.Parallel()
		m, rec, _ := setup(t)
		res := &fakeResource{status: rns.ResourceStatusComplete, data: []byte("tampered!!")}
		m.BindStartedResource(link, res)
		m.OnResourceConcluded(link, res)

		if rec.stats["resources_received"] != 0 {
			t.Errorf("stats = %+v, want no resources_received", rec.stats)
		}
		if exp := m.GetResourceExpectationByRID(link, rid); exp == nil {
			t.Error("sha mismatch removed the expectation")
		}
		if len(rec.sent) != 0 {
			t.Errorf("mismatched transfer sent = %+v, want none", rec.sent)
		}
	})

	t.Run("no matching expectation is a no-op", func(t *testing.T) {
		t.Parallel()
		m, rec, _ := setup(t)
		res := &fakeResource{status: rns.ResourceStatusComplete, data: []byte("unbound data")}
		m.BindStartedResource(link, res)
		m.OnResourceConcluded(link, res)

		if rec.stats["resources_received"] != 0 {
			t.Errorf("stats = %+v, want no resources_received", rec.stats)
		}
		if len(rec.sent) != 0 {
			t.Errorf("unbound transfer sent = %+v, want none", rec.sent)
		}
	})
}

// G10.5 DispatchReceivedResource mirrors _dispatch_received_resource: the
// notice kind forwards to room members except the sender, the motd and
// blob kinds log only, and unknown kinds warn.
func TestDispatchReceivedResource(t *testing.T) {
	t.Parallel()

	link := &rns.Link{}
	peer := bytesOf(0xcc, 32)
	room := "general"
	memberA := &rns.Link{}
	memberB := &rns.Link{}

	newDispatchEnv := func(t *testing.T) (*ResourceManager, *resourceRecorder) {
		t.Helper()
		rec := &resourceRecorder{stats: map[string]int{}}
		m, _ := newTestResourceManager(t, func(h *ResourceHooks) {
			h.StatsInc = func(key string, delta int) { rec.stats[key] += delta }
			h.SendPacket = func(l *rns.Link, p []byte) error {
				rec.sent = append(rec.sent, sentPacket{link: l, payload: p})
				return nil
			}
			h.GetSessionPeer = func(*rns.Link) []byte { return peer }
			h.GetRoomMembers = func(string) map[*rns.Link]bool {
				return map[*rns.Link]bool{link: true, memberA: true, memberB: true}
			}
		})
		return m, rec
	}

	t.Run("notice forwards to members except the sender", func(t *testing.T) {
		t.Parallel()
		m, rec := newDispatchEnv(t)
		exp := &ResourceExpectation{ID: []byte{1}, Kind: ResKindNotice, Room: &room}
		m.DispatchReceivedResource(link, exp, []byte("a large notice body"))

		if len(rec.sent) != 2 {
			t.Fatalf("notice dispatch sent = %+v, want two forwarded envelopes", rec.sent)
		}
		for _, p := range rec.sent {
			if p.link == link {
				t.Error("notice was forwarded back to the sender")
			}
			decoded, err := cbor.Decode(p.payload)
			if err != nil {
				t.Fatalf("forwarded payload does not decode: %v", err)
			}
			mm, ok := decoded.(*cbor.Map)
			if !ok {
				t.Fatal("forwarded payload is not a CBOR map")
			}
			if v, _ := mm.Get(KT); !int64Equal(v, int64(TNotice)) {
				t.Errorf("forwarded type = %v, want NOTICE", v)
			}
			if src, _ := mm.Get(KSrc); !sameBytes(src.([]byte), peer) {
				t.Errorf("forwarded src = %v, want the peer hash", src)
			}
			if r, _ := mm.Get(KRoom); r != room {
				t.Errorf("forwarded room = %v, want %v", r, room)
			}
			if body, _ := mm.Get(KBody); body != "a large notice body" {
				t.Errorf("forwarded body = %v, want the decoded text", body)
			}
		}
		if rec.stats["notices_forwarded"] != 1 {
			t.Errorf("notices_forwarded = %v, want 1", rec.stats["notices_forwarded"])
		}
	})

	t.Run("motd and blob only log", func(t *testing.T) {
		t.Parallel()
		m, rec := newDispatchEnv(t)
		m.DispatchReceivedResource(link, &ResourceExpectation{ID: []byte{1}, Kind: ResKindMOTD}, []byte("motd text"))
		m.DispatchReceivedResource(link, &ResourceExpectation{ID: []byte{2}, Kind: ResKindBlob}, []byte{0, 1, 2})
		m.DispatchReceivedResource(link, &ResourceExpectation{ID: []byte{3}, Kind: "weird"}, []byte("x"))
		if len(rec.sent) != 0 {
			t.Errorf("non-notice dispatch sent = %+v, want none", rec.sent)
		}
		if rec.stats["notices_forwarded"] != 0 {
			t.Errorf("notices_forwarded = %v, want 0", rec.stats["notices_forwarded"])
		}
	})

	t.Run("notice without a room or identity does not forward", func(t *testing.T) {
		t.Parallel()
		m, rec := newDispatchEnv(t)
		m.DispatchReceivedResource(link, &ResourceExpectation{ID: []byte{1}, Kind: ResKindNotice}, []byte("no room"))
		if len(rec.sent) != 0 {
			t.Errorf("room-less dispatch sent = %+v, want none", rec.sent)
		}
	})
}

// G10.6 SendViaResource mirrors send_via_resource: the disabled and
// oversized rejections, the immediate envelope send with the exact body
// key order, the resource hand-off, the active-resource registration,
// and the failure paths.
func TestSendViaResource(t *testing.T) {
	t.Parallel()

	link := &rns.Link{}
	payload := []byte("large payload contents")

	newSendEnv := func(t *testing.T, mutate func(*ResourceHooks)) (*ResourceManager, *resourceRecorder, *fakeResource, *bool) {
		t.Helper()
		rec := &resourceRecorder{stats: map[string]int{}}
		fake := &fakeResource{status: rns.ResourceStatusComplete}
		sentResource := false
		m, _ := newTestResourceManager(t, func(h *ResourceHooks) {
			h.StatsInc = func(key string, delta int) { rec.stats[key] += delta }
			h.SendPacket = func(l *rns.Link, p []byte) error {
				rec.sent = append(rec.sent, sentPacket{link: l, payload: p})
				return nil
			}
			h.SendResource = func(p []byte, l *rns.Link) (ResourceHandle, error) {
				sentResource = true
				return fake, nil
			}
			if mutate != nil {
				mutate(h)
			}
		})
		return m, rec, fake, &sentResource
	}

	t.Run("disabled returns false", func(t *testing.T) {
		t.Parallel()
		m, rec, _, sentResource := newSendEnv(t, func(h *ResourceHooks) {
			h.EnableResourceTransfer = func() bool { return false }
		})
		if m.SendViaResource(link, ResKindNotice, payload, nil, "") {
			t.Error("SendViaResource with transfer disabled = true, want false")
		}
		if len(rec.sent) != 0 || *sentResource {
			t.Errorf("disabled send produced output: %v %v", rec.sent, *sentResource)
		}
	})

	t.Run("oversized returns false", func(t *testing.T) {
		t.Parallel()
		m, rec, _, sentResource := newSendEnv(t, func(h *ResourceHooks) {
			h.MaxResourceBytes = func() int { return 4 }
		})
		if m.SendViaResource(link, ResKindNotice, payload, nil, "") {
			t.Error("oversized SendViaResource = true, want false")
		}
		if len(rec.sent) != 0 || *sentResource {
			t.Errorf("oversized send produced output: %v %v", rec.sent, *sentResource)
		}
	})

	t.Run("success sends the envelope then the resource", func(t *testing.T) {
		t.Parallel()
		room := "general"
		m, rec, fake, sentResource := newSendEnv(t, nil)
		if !m.SendViaResource(link, ResKindNotice, payload, &room, "utf-8") {
			t.Fatal("SendViaResource = false, want true")
		}
		if len(rec.sent) != 1 {
			t.Fatalf("send output = %+v, want one envelope", rec.sent)
		}
		decoded, err := cbor.Decode(rec.sent[0].payload)
		if err != nil {
			t.Fatalf("envelope does not decode: %v", err)
		}
		envMap, ok := decoded.(*cbor.Map)
		if !ok {
			t.Fatal("envelope is not a CBOR map")
		}
		// Envelope keys: 0,1,2,3,4 (base) then 5 (room), 6 (body).
		if got := envMap.Pairs(); len(got) != 7 {
			t.Fatalf("envelope has %v keys, want 7", len(got))
		}
		body, _ := envMap.Get(KBody)
		bodyMap, ok := body.(*cbor.Map)
		if !ok {
			t.Fatalf("body = %T, want a CBOR map", body)
		}
		pairs := bodyMap.Pairs()
		if len(pairs) != 5 {
			t.Fatalf("body has %v keys, want 5 (id, kind, size, sha, encoding)", len(pairs))
		}
		wantKeys := []int64{BResID, BResKind, BResSize, BResSHA256, BResEncoding}
		for i, pair := range pairs {
			key, isInt := intValue(pair.Key)
			if !isInt || key != wantKeys[i] {
				t.Errorf("body key %v = %v, want %v", i, pair.Key, wantKeys[i])
			}
		}
		rid, _ := bodyMap.Get(BResID)
		ridBytes, isBytes := rid.([]byte)
		if !isBytes || len(ridBytes) != 8 {
			t.Errorf("rid = %#v, want 8 random bytes", rid)
		}
		kind, _ := bodyMap.Get(BResKind)
		if kind != ResKindNotice {
			t.Errorf("kind = %v, want %v", kind, ResKindNotice)
		}
		size, _ := bodyMap.Get(BResSize)
		sizeInt, isInt := intValue(size)
		if !isInt || sizeInt != int64(len(payload)) {
			t.Errorf("size = %v, want %v", size, len(payload))
		}
		sha, _ := bodyMap.Get(BResSHA256)
		if !sameBytes(sha.([]byte), sha256Digest(payload)) {
			t.Errorf("sha = %v, want the payload digest", sha)
		}
		enc, _ := bodyMap.Get(BResEncoding)
		if enc != "utf-8" {
			t.Errorf("encoding = %v, want utf-8", enc)
		}
		if rec.stats["bytes_out"] != len(rec.sent[0].payload) {
			t.Errorf("bytes_out = %v, want %v", rec.stats["bytes_out"], len(rec.sent[0].payload))
		}
		if !*sentResource {
			t.Error("SendResource was not called")
		}
		if rec.stats["resources_sent"] != 1 || rec.stats["resource_bytes_sent"] != len(payload) {
			t.Errorf("stats = %+v, want resources_sent=1 bytes=%v", rec.stats, len(payload))
		}
		if !m.HasActiveResource(link, fake) {
			t.Error("the sent resource is not tracked as active")
		}
	})

	t.Run("empty encoding is omitted", func(t *testing.T) {
		t.Parallel()
		m, rec, _, _ := newSendEnv(t, nil)
		if !m.SendViaResource(link, ResKindNotice, payload, nil, "") {
			t.Fatal("SendViaResource = false, want true")
		}
		decoded, err := cbor.Decode(rec.sent[0].payload)
		if err != nil {
			t.Fatalf("envelope does not decode: %v", err)
		}
		envMap := decoded.(*cbor.Map)
		bodyMap, _ := envMap.Get(KBody)
		body := bodyMap.(*cbor.Map)
		if _, present := body.Get(BResEncoding); present {
			t.Error("empty encoding was encoded into the body")
		}
	})

	t.Run("envelope send failure returns false", func(t *testing.T) {
		t.Parallel()
		m, rec, _, sentResource := newSendEnv(t, func(h *ResourceHooks) {
			h.SendPacket = func(*rns.Link, []byte) error { return errors.New("boom") }
		})
		if m.SendViaResource(link, ResKindNotice, payload, nil, "") {
			t.Error("SendViaResource with a send failure = true, want false")
		}
		if *sentResource {
			t.Error("SendResource ran after an envelope send failure")
		}
		if len(rec.sent) != 0 {
			t.Errorf("failed send recorded output: %v", rec.sent)
		}
	})

	t.Run("resource creation failure returns false", func(t *testing.T) {
		t.Parallel()
		m, rec, _, _ := newSendEnv(t, func(h *ResourceHooks) {
			h.SendResource = func([]byte, *rns.Link) (ResourceHandle, error) {
				return nil, errors.New("no resource")
			}
		})
		if m.SendViaResource(link, ResKindNotice, payload, nil, "") {
			t.Error("SendViaResource with a resource failure = true, want false")
		}
		if len(rec.sent) != 1 {
			t.Errorf("failed resource send output = %+v, want the already-sent envelope", rec.sent)
		}
		if rec.stats["resources_sent"] != 0 {
			t.Errorf("resources_sent = %v, want 0", rec.stats["resources_sent"])
		}
	})
}

// int64Equal reports whether v is the int value n.
func int64Equal(v any, n int64) bool {
	got, ok := intValue(v)
	return ok && got == n
}

// AcceptAdvertisedResource mirrors _resource_advertised: the
// disabled/oversized/session-less/expectation-less rejections each count
// resources_rejected, and an accepted advertisement leaves the matched
// expectation id pending for the started callback.
func TestAcceptAdvertisedResource(t *testing.T) {
	t.Parallel()

	link := &rns.Link{}
	setup := func(t *testing.T, mutate func(*ResourceHooks)) (*ResourceManager, *resourceRecorder) {
		t.Helper()
		rec := &resourceRecorder{stats: map[string]int{}}
		m, _ := newTestResourceManager(t, func(h *ResourceHooks) {
			h.StatsInc = func(key string, delta int) { rec.stats[key] += delta }
			if mutate != nil {
				mutate(h)
			}
		})
		if !m.AddResourceExpectation(link, []byte{1, 2}, ResKindNotice, 10, nil, "", nil) {
			t.Fatal("AddResourceExpectation() = false, want true")
		}
		return m, rec
	}

	t.Run("disabled rejects with the counter", func(t *testing.T) {
		t.Parallel()
		m, rec := setup(t, func(h *ResourceHooks) {
			h.EnableResourceTransfer = func() bool { return false }
		})
		if m.AcceptAdvertisedResource(link, 10) {
			t.Error("AcceptAdvertisedResource with transfer disabled = true, want false")
		}
		if rec.stats["resources_rejected"] != 1 {
			t.Errorf("resources_rejected = %v, want 1", rec.stats["resources_rejected"])
		}
	})

	t.Run("oversized rejects", func(t *testing.T) {
		t.Parallel()
		m, rec := setup(t, func(h *ResourceHooks) {
			h.MaxResourceBytes = func() int { return 4 }
		})
		if m.AcceptAdvertisedResource(link, 10) {
			t.Error("AcceptAdvertisedResource oversized = true, want false")
		}
		if rec.stats["resources_rejected"] != 1 {
			t.Errorf("resources_rejected = %v, want 1", rec.stats["resources_rejected"])
		}
	})

	t.Run("no session rejects", func(t *testing.T) {
		t.Parallel()
		m, rec := setup(t, func(h *ResourceHooks) {
			h.HasSession = func(*rns.Link) bool { return false }
		})
		if m.AcceptAdvertisedResource(link, 10) {
			t.Error("AcceptAdvertisedResource without a session = true, want false")
		}
		if rec.stats["resources_rejected"] != 1 {
			t.Errorf("resources_rejected = %v, want 1", rec.stats["resources_rejected"])
		}
	})

	t.Run("no matching size rejects", func(t *testing.T) {
		t.Parallel()
		m, rec := setup(t, nil)
		if m.AcceptAdvertisedResource(link, 999) {
			t.Error("AcceptAdvertisedResource without an expectation = true, want false")
		}
		if rec.stats["resources_rejected"] != 1 {
			t.Errorf("resources_rejected = %v, want 1", rec.stats["resources_rejected"])
		}
	})

	t.Run("matching size accepts and pends the expectation", func(t *testing.T) {
		t.Parallel()
		m, rec := setup(t, nil)
		if !m.AcceptAdvertisedResource(link, 10) {
			t.Fatal("AcceptAdvertisedResource = false, want true")
		}
		if rec.stats["resources_rejected"] != 0 {
			t.Errorf("resources_rejected = %v, want 0", rec.stats["resources_rejected"])
		}
		res := &fakeResource{status: rns.ResourceStatusComplete, data: bytes.Repeat([]byte{0}, 10)}
		m.BindStartedResource(link, res)
		if !m.HasActiveResource(link, res) {
			t.Error("the accepted resource was not registered as active")
		}
		m.OnResourceConcluded(link, res)
		if exp := m.GetResourceExpectationByRID(link, []byte{1, 2}); exp != nil {
			t.Errorf("expectation survived the concluded transfer: %+v", exp)
		}
	})
}

// G10.7 StartResourceCleanupLoop mirrors _resource_cleanup_loop: a fixed
// 30-second interval, a cleanup pass each cycle, and the shutdown exit.
// The simulated sleep hook advances the clock and reports whether the
// loop should keep running.
func TestResourceCleanupLoop(t *testing.T) {
	t.Parallel()

	link := &rns.Link{}
	now := 100.0
	m, _ := newTestResourceManager(t, func(h *ResourceHooks) {
		h.Now = func() float64 { return now }
	})
	rid := []byte{9, 9}
	if !m.AddResourceExpectation(link, rid, ResKindNotice, 5, nil, "", nil) {
		t.Fatal("AddResourceExpectation() = false, want true")
	}

	stop := make(chan struct{})
	cycles := 0
	sleep := func(interval float64) bool {
		cycles++
		if cycles > 2 {
			// The shutdown signal fired during the third sleep.
			close(stop)
			return false
		}
		if interval != 30.0 {
			t.Errorf("sleep interval = %v, want 30", interval)
		}
		// Advance the clock past the expectation TTL (100 + 30).
		now += 31.0
		return true
	}

	done := make(chan struct{})
	go func() {
		m.StartResourceCleanupLoop(stop, sleep)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(stop)
		t.Fatal("cleanup loop did not exit after the shutdown signal")
	}

	if exp := m.GetResourceExpectationByRID(link, rid); exp != nil {
		t.Errorf("expectation survived the cleanup cycles: %+v", exp)
	}
}
