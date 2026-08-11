// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

// TestBlackholeUpdaterConstants asserts the BlackholeUpdater timing
// constants equal the values captured from Python
// RNS.Discovery.BlackholeUpdater (Discovery.py:636-639, rns 1.3.5).
func TestBlackholeUpdaterConstants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  time.Duration
		want time.Duration
	}{
		{"BlackholeInitialWait", BlackholeInitialWait, 20 * time.Second},
		{"BlackholeJobInterval", BlackholeJobInterval, 60 * time.Second},
		{"BlackholeUpdateInterval", BlackholeUpdateInterval, 1 * 60 * 60 * time.Second},
		{"BlackholeSourceTimeout", BlackholeSourceTimeout, 25 * time.Second},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}

// fakeBlackholeSource is a stand-in remote source serving a fixed list via
// the injected fetch function, with per-source call counting.
type fakeBlackholeSource struct {
	mu    sync.Mutex
	lists map[string][]blackholeListEntry
	calls map[string]int
}

func (f *fakeBlackholeSource) fetch(sourceIdentityHash []byte) ([]blackholeListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := string(sourceIdentityHash)
	f.calls[key]++
	list := f.lists[key]
	out := make([]blackholeListEntry, len(list))
	copy(out, list)
	return out, nil
}

func (f *fakeBlackholeSource) callCount(sourceIdentityHash []byte) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[string(sourceIdentityHash)]
}

// TestBlackholeUpdaterGolden verifies the BlackholeUpdater loop core
// (runJobPass) against the Python Discovery.BlackholeUpdater behaviour
// (Discovery.py:655-720) with an injected clock and a fake source:
//   - the first pass fetches every configured source and merges its list,
//     persisting the fetched list to blackholepath/<hex(source)>;
//   - a pass within UPDATE_INTERVAL does not refetch (per-source timing);
//   - a pass after UPDATE_INTERVAL refetches;
//   - existing entries (including own-sourced) are never overwritten by a
//     fetched entry (Python's `if not identity_hash in ...` guard);
//   - the persisted file is the msgpack bin-keyed map Python writes.
func TestBlackholeUpdaterGolden(t *testing.T) {
	t.Parallel()

	own := mustHexDecode(t, "aabbccddeeff00112233445566778899")
	srcA := mustHexDecode(t, "11111111111111111111111111111111")
	srcB := mustHexDecode(t, "22222222222222222222222222222222")
	ih1 := mustHexDecode(t, "deadbeefdeadbeefdeadbeefdeadbeef")
	ih2 := mustHexDecode(t, "cafebabecafebabecafebabecafebabe")
	ih3 := mustHexDecode(t, "feedfacefeedfacefeedfacefeedface")

	ts := NewTransportSystem(testSilentLogger())
	ts.identity = &Identity{Hash: own}
	ts.blackholePath = t.TempDir()

	// Pre-seed ih1 as locally-sourced; the updater must not overwrite it
	// when srcA's list also contains ih1.
	ts.blackholedIdentities[string(ih1)] = BlackholeIdentityEntry{
		IdentityHash: ih1, Source: own, Reason: "preseed-own",
	}

	until2 := time.Unix(9_900_000_000, 0)
	fake := &fakeBlackholeSource{
		lists: map[string][]blackholeListEntry{
			string(srcA): {
				{identityHash: ih1, source: srcA, until: nil, reason: "srcA-ih1"}, // already present -> skipped
				{identityHash: ih2, source: srcA, until: &until2, reason: "srcA-ih2"},
			},
			string(srcB): {
				{identityHash: ih3, source: srcB, until: nil, reason: "srcB-ih3"},
			},
		},
		calls: map[string]int{},
	}

	sources := [][]byte{srcA, srcB}
	updater := NewBlackholeUpdater(ts, func() [][]byte { return sources }, fake.fetch)

	// Use a realistic clock base so the zero-value lastUpdates (1970) is
	// always older than UPDATE_INTERVAL on the first pass, exactly as
	// Python's time.time()-based gate behaves.
	t0 := time.Unix(1_800_000_000, 0)

	// First pass: both sources fetched, new entries merged.
	if got := updater.runJobPass(t0); got != 2 {
		t.Fatalf("first pass added %d, want 2 (ih2 + ih3; ih1 already present)", got)
	}
	if got, want := fake.callCount(srcA), 1; got != want {
		t.Fatalf("srcA call count after first pass = %d, want %d", got, want)
	}
	if got, want := fake.callCount(srcB), 1; got != want {
		t.Fatalf("srcB call count after first pass = %d, want %d", got, want)
	}

	// ih1 must remain the preseeded own-sourced entry (not overwritten).
	if e, ok := ts.blackholedIdentities[string(ih1)]; !ok || string(e.Source) != string(own) || e.Reason != "preseed-own" {
		t.Fatalf("ih1 overwritten by updater: %+v", e)
	}
	// ih2 and ih3 must have been merged from the fetched lists.
	if e, ok := ts.blackholedIdentities[string(ih2)]; !ok || string(e.Source) != string(srcA) || e.Reason != "srcA-ih2" {
		t.Fatalf("ih2 not merged correctly: %+v", e)
	}
	if e := ts.blackholedIdentities[string(ih2)]; e.Until == nil || e.Until.Unix() != 9_900_000_000 {
		t.Fatalf("ih2 until not preserved: %+v", e.Until)
	}
	if e, ok := ts.blackholedIdentities[string(ih3)]; !ok || string(e.Source) != string(srcB) || e.Reason != "srcB-ih3" {
		t.Fatalf("ih3 not merged correctly: %+v", e)
	}

	// Each source's fetched list must be persisted to
	// blackholepath/<hex(source)> as the msgpack bin-keyed map Python
	// writes (Discovery.py:681-690).
	assertPersistedSourceList(t, ts.blackholePath, srcA, fake.lists[string(srcA)])
	assertPersistedSourceList(t, ts.blackholePath, srcB, fake.lists[string(srcB)])

	// Second pass one JOB_INTERVAL later: within UPDATE_INTERVAL, no fetch.
	if got := updater.runJobPass(t0.Add(BlackholeJobInterval)); got != 0 {
		t.Fatalf("second pass added %d, want 0 (within UPDATE_INTERVAL)", got)
	}
	if got, want := fake.callCount(srcA), 1; got != want {
		t.Fatalf("srcA refetched within UPDATE_INTERVAL: call count = %d, want %d", got, want)
	}
	if got, want := fake.callCount(srcB), 1; got != want {
		t.Fatalf("srcB refetched within UPDATE_INTERVAL: call count = %d, want %d", got, want)
	}

	// Third pass just past UPDATE_INTERVAL: both sources refetched. The
	// lists are unchanged so no new entries are merged (added == 0), but
	// the fetch still happens (call counts advance).
	if got := updater.runJobPass(t0.Add(BlackholeUpdateInterval + 1)); got != 0 {
		t.Fatalf("third pass added %d, want 0 (entries already present)", got)
	}
	if got, want := fake.callCount(srcA), 2; got != want {
		t.Fatalf("srcA call count after UPDATE_INTERVAL = %d, want %d", got, want)
	}
	if got, want := fake.callCount(srcB), 2; got != want {
		t.Fatalf("srcB call count after UPDATE_INTERVAL = %d, want %d", got, want)
	}
}

// assertPersistedSourceList reads blackholepath/<hex(source)> and asserts
// the decoded msgpack map carries every entry of the fetched list with the
// correct source/until/reason, independent of map key order.
func assertPersistedSourceList(t *testing.T, blackholePath string, source []byte, list []blackholeListEntry) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(blackholePath, hex.EncodeToString(source)))
	if err != nil {
		t.Fatalf("reading persisted source list for %x: %v", source, err)
	}
	obj, err := msgpack.Unpack(data)
	if err != nil {
		t.Fatalf("unpacking persisted source list for %x: %v", source, err)
	}
	m, ok := obj.(map[any]any)
	if !ok {
		t.Fatalf("persisted source list for %x type %T, want map", source, obj)
	}
	if got, want := len(m), len(list); got != want {
		t.Fatalf("persisted source list for %x has %d entries, want %d", source, got, want)
	}
	for _, e := range list {
		var until any
		if e.until != nil {
			until = float64(e.until.Unix()) + float64(e.until.Nanosecond())/1e9
		}
		assertBlackholeSubEntry(t, m, e.identityHash, e.source, until, e.reason)
	}
}

// TestBlackholeUpdaterStartStop exercises the production loop with a fake
// source and a controllable clock, asserting Start runs passes and Stop
// halts them. It uses a shortened jobInterval via a real-time ticker, so it
// keeps the wall-clock cost bounded.
func TestBlackholeUpdaterStartStop(t *testing.T) {
	t.Parallel()

	own := mustHexDecode(t, "aabbccddeeff00112233445566778899")
	srcA := mustHexDecode(t, "11111111111111111111111111111111")
	ih2 := mustHexDecode(t, "cafebabecafebabecafebabecafebabe")

	ts := NewTransportSystem(testSilentLogger())
	ts.identity = &Identity{Hash: own}
	ts.blackholePath = t.TempDir()

	var calls atomic.Int32
	fetch := func(sourceIdentityHash []byte) ([]blackholeListEntry, error) {
		calls.Add(1)
		return []blackholeListEntry{{identityHash: ih2, source: srcA, until: nil, reason: "srcA-ih2"}}, nil
	}

	updater := NewBlackholeUpdater(ts, func() [][]byte { return [][]byte{srcA} }, fetch)
	// Shorten the waits so the test runs in well under a second.
	updater.initialWait = 20 * time.Millisecond
	updater.jobInterval = 20 * time.Millisecond
	updater.updateInterval = 1 * time.Millisecond

	updater.Start()
	// Wait for at least one fetch to occur.
	deadline := time.After(2 * time.Second)
	for {
		if calls.Load() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("updater loop never fetched; calls=%d", calls.Load())
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	updater.Stop()

	// After Stop, the loop must not fetch again. Give it a moment and
	// confirm the count is stable.
	callsAtStop := calls.Load()
	time.Sleep(80 * time.Millisecond)
	if got := calls.Load(); got != callsAtStop {
		t.Fatalf("updater fetched after Stop: calls went %d -> %d", callsAtStop, got)
	}
}

// TestBlackholeUpdaterSetUpdateInterval verifies SetUpdateInterval is honored
// by runJobPass: with a 2-minute interval (the [reticulum]
// blackhole_update_interval clamp floor, RNS/Reticulum.py:601-604), a source
// fetched at t0 is not refetched at t0+90s but is refetched at t0+121s.
func TestBlackholeUpdaterSetUpdateInterval(t *testing.T) {
	t.Parallel()

	src := mustHexDecode(t, "11111111111111111111111111111111")
	ih := mustHexDecode(t, "cafebabecafebabecafebabecafebabe")
	own := mustHexDecode(t, "aabbccddeeff00112233445566778899")

	ts := NewTransportSystem(testSilentLogger())
	ts.identity = &Identity{Hash: own}
	ts.blackholePath = t.TempDir()

	var calls atomic.Int32
	fetch := func([]byte) ([]blackholeListEntry, error) {
		calls.Add(1)
		return []blackholeListEntry{{identityHash: ih, source: src, until: nil, reason: "x"}}, nil
	}
	updater := NewBlackholeUpdater(ts, func() [][]byte { return [][]byte{src} }, fetch)
	updater.SetUpdateInterval(2 * time.Minute)
	if got := updater.UpdateInterval(); got != 2*time.Minute {
		t.Fatalf("UpdateInterval() = %v, want 2m", got)
	}

	t0 := time.Unix(1_800_000_000, 0)
	if got := updater.runJobPass(t0); got != 1 {
		t.Fatalf("first pass added %d, want 1", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("after first pass calls = %d, want 1", got)
	}
	// 90s later: within the 2-minute interval → no refetch.
	if got := updater.runJobPass(t0.Add(90 * time.Second)); got != 0 {
		t.Fatalf("within-interval pass added %d, want 0", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("within-interval refetched: calls = %d, want 1", got)
	}
	// 121s later: past the 2-minute interval → refetch (entries unchanged → 0 added).
	if got := updater.runJobPass(t0.Add(121 * time.Second)); got != 0 {
		t.Fatalf("past-interval pass added %d, want 0 (entries already present)", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("past-interval did not refetch: calls = %d, want 2", got)
	}
}
