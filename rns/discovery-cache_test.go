// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// discoveryHandlerFixture builds a standalone InterfaceAnnounceHandler (with a
// minimal Reticulum) and a valid discovery announce of the given stamp cost,
// ready to feed to receivedAnnounce.
func discoveryHandlerFixture(t *testing.T, requiredValue, stampCost int) (*InterfaceAnnounceHandler, []byte, *Identity) {
	t.Helper()
	tmpDir := testutils.TempDir(t, "rns-discovery-cache-")
	ts := NewTransportSystem(nil)
	r := &Reticulum{
		configDir: tmpDir,
		transport: ts,
		logger:    NewLogger(),
	}
	h := NewInterfaceAnnounceHandler(r, requiredValue, func(info map[string]any) {})
	appData := mustDiscoveryAnnounceAppData(t, map[any]any{
		discoveryFieldInterfaceType: "TCPServerInterface",
		discoveryFieldTransport:     true,
		discoveryFieldTransportID:   discoveryTestTransportID,
		discoveryFieldName:          "Cached TCP",
		discoveryFieldReachableOn:   "discovery.example.net",
		discoveryFieldPort:          4242,
	}, stampCost)
	return h, appData, mustTestNewIdentity(t, true)
}

// TestDiscoveryValidCacheServesSecondHit covers Phase 11 task 1: feeding the
// same discovery announce twice must serve the second hit from validCache
// (Python Discovery.valid_cache, Discovery.py:264-272) — the callback still
// fires, but no re-validation occurs, observable as a single validCache
// entry and one cache hit.
func TestDiscoveryValidCacheServesSecondHit(t *testing.T) {
	t.Parallel()
	h, appData, sourceIdentity := discoveryHandlerFixture(t, 2, 2)
	destinationHash := []byte("discovery-destination")

	h.receivedAnnounce(destinationHash, sourceIdentity, appData)
	if len(h.validCache) != 1 {
		t.Fatalf("after first announce: validCache len=%v, want 1", len(h.validCache))
	}
	if h.validCacheHits != 0 {
		t.Fatalf("after first announce: validCacheHits=%v, want 0", h.validCacheHits)
	}

	// Second identical announce must be a cache hit (no new entry).
	h.receivedAnnounce(destinationHash, sourceIdentity, appData)
	if len(h.validCache) != 1 {
		t.Fatalf("after second announce: validCache len=%v, want 1 (hit reuses entry)", len(h.validCache))
	}
	if h.validCacheHits != 1 {
		t.Fatalf("after second announce: validCacheHits=%v, want 1 (served from cache)", h.validCacheHits)
	}
}

// TestDiscoveryInvalidCacheDropsRepeat covers Phase 11 task 1: an announce
// with an insufficient stamp value is appended to invalidCache (Python
// Discovery.py:293-294), and a second identical announce is dropped from the
// invalid cache without invoking the callback or re-validating.
func TestDiscoveryInvalidCacheDropsRepeat(t *testing.T) {
	t.Parallel()
	// requiredValue 20 but stamp cost only 2 -> invalid.
	h, appData, sourceIdentity := discoveryHandlerFixture(t, 20, 2)
	destinationHash := []byte("discovery-destination")

	h.receivedAnnounce(destinationHash, sourceIdentity, appData)
	if len(h.invalidCache) != 1 {
		t.Fatalf("after invalid announce: invalidCache len=%v, want 1", len(h.invalidCache))
	}
	if h.invalidCacheHits != 0 {
		t.Fatalf("after invalid announce: invalidCacheHits=%v, want 0", h.invalidCacheHits)
	}

	// Second identical announce must be dropped from the invalid cache.
	h.receivedAnnounce(destinationHash, sourceIdentity, appData)
	if h.invalidCacheHits != 1 {
		t.Fatalf("after repeat invalid announce: invalidCacheHits=%v, want 1 (dropped from cache)", h.invalidCacheHits)
	}
	if len(h.invalidCache) != 1 {
		t.Fatalf("after repeat invalid announce: invalidCache len=%v, want 1 (no duplicate)", len(h.invalidCache))
	}
}

// TestDiscoveryValidCacheEvictsAtMax covers Phase 11 task 1's FIFO eviction
// (Python Discovery.py:290): the valid cache holds at most validCacheMax
// entries, evicting the oldest first.
func TestDiscoveryValidCacheEvictsAtMax(t *testing.T) {
	t.Parallel()
	h, _, sourceIdentity := discoveryHandlerFixture(t, 2, 2)
	destinationHash := []byte("discovery-destination")
	// Shrink the cap so the test is fast.
	h.validCacheMax = 4

	payloads := []map[any]any{
		{discoveryFieldInterfaceType: "TCPServerInterface", discoveryFieldTransport: true,
			discoveryFieldTransportID: discoveryTestTransportIDFromByte(1), discoveryFieldName: "I1",
			discoveryFieldReachableOn: "h1", discoveryFieldPort: 1},
		{discoveryFieldInterfaceType: "TCPServerInterface", discoveryFieldTransport: true,
			discoveryFieldTransportID: discoveryTestTransportIDFromByte(2), discoveryFieldName: "I2",
			discoveryFieldReachableOn: "h2", discoveryFieldPort: 2},
		{discoveryFieldInterfaceType: "TCPServerInterface", discoveryFieldTransport: true,
			discoveryFieldTransportID: discoveryTestTransportIDFromByte(3), discoveryFieldName: "I3",
			discoveryFieldReachableOn: "h3", discoveryFieldPort: 3},
		{discoveryFieldInterfaceType: "TCPServerInterface", discoveryFieldTransport: true,
			discoveryFieldTransportID: discoveryTestTransportIDFromByte(4), discoveryFieldName: "I4",
			discoveryFieldReachableOn: "h4", discoveryFieldPort: 4},
		{discoveryFieldInterfaceType: "TCPServerInterface", discoveryFieldTransport: true,
			discoveryFieldTransportID: discoveryTestTransportIDFromByte(5), discoveryFieldName: "I5",
			discoveryFieldReachableOn: "h5", discoveryFieldPort: 5},
	}
	for _, p := range payloads {
		ad := mustDiscoveryAnnounceAppData(t, p, 2)
		h.receivedAnnounce(destinationHash, sourceIdentity, ad)
	}
	if len(h.validCache) > h.validCacheMax {
		t.Fatalf("validCache len=%v, want <= %v (eviction)", len(h.validCache), h.validCacheMax)
	}
	if len(h.validCache) != h.validCacheMax {
		t.Fatalf("validCache len=%v, want %v (full after eviction)", len(h.validCache), h.validCacheMax)
	}
	// The oldest (first) payload must have been evicted; the newest four
	// remain. The first payload's fullhash must not be in the cache.
	first := mustDiscoveryAnnounceAppData(t, payloads[0], 2)
	if _, ok := h.validCache[string(FullHash(first[1:]))]; ok {
		t.Fatalf("oldest entry not evicted (FIFO eviction broken)")
	}
}
