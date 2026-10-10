// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns/interfaces"
)

// TestAnnounceReplacesUnresponsivePathWithShorterOrEqualHops verifies that an
// incoming announce with the same timebase replaces an existing unresponsive
// path when hops are shorter or equal, even if the interface gravity is identical.
// Mirrors Python RNS/Transport.py:1885-1895.
func TestAnnounceReplacesUnresponsivePathWithShorterOrEqualHops(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)

	ifaceLoRa := newAFI("LoRa-RNode", interfaces.ModeFull)
	ifaceTCP := newAFI("Local TCP Hub", interfaces.ModeFull)

	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "hub-dest")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	emission := uint64(time.Now().Unix())

	// First announce over LoRa: wire hops=1 -> hops=2.
	p1 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, emission)
	p1.Hops = 1
	if err := p1.Pack(); err != nil {
		t.Fatalf("Pack p1: %v", err)
	}
	ts.Inbound(append([]byte(nil), p1.Raw...), ifaceLoRa)

	ts.mu.Lock()
	entry, ok := ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok || entry.Hops != 2 {
		t.Fatalf("initial path hops = %v, ok = %v, want hops=2", entry.Hops, ok)
	}

	// Mark path unresponsive (e.g. after a communication failure/timeout).
	ts.MarkPathUnresponsive(dest.Hash)

	ts.mu.Lock()
	if !ts.pathTable[string(dest.Hash)].Unresponsive {
		ts.mu.Unlock()
		t.Fatal("path was not marked unresponsive")
	}
	ts.mu.Unlock()

	// Second announce arrives over TCP with wire hops=0 -> hops=1 (shorter path),
	// same emission timebase, same gravity (0).
	p2 := mustTestAnnouncePacketWithEmission(t, ts, id, dest, emission)
	p2.Hops = 0
	if err := p2.Pack(); err != nil {
		t.Fatalf("Pack p2: %v", err)
	}
	ts.Inbound(append([]byte(nil), p2.Raw...), ifaceTCP)

	ts.mu.Lock()
	entry, ok = ts.pathTable[string(dest.Hash)]
	ts.mu.Unlock()
	if !ok {
		t.Fatal("path table entry disappeared")
	}
	if entry.Hops != 1 {
		t.Errorf("after TCP announce: hops = %v, want 1", entry.Hops)
	}
	if entry.Interface != ifaceTCP {
		t.Errorf("after TCP announce: interface = %v, want %v", entry.Interface.Name(), ifaceTCP.Name())
	}
	if entry.Unresponsive {
		t.Error("after TCP announce: path is still unresponsive, want it cleared to unknown/responsive")
	}
}

// TestPendingLinkTimeoutCleansUpAndExpiresPath verifies that when a pending link
// times out on a non-transport node, processPendingLinks prunes the closed link,
// expires the dead path from the path table, and triggers path rediscovery.
// Mirrors Python RNS/Transport.py:530-575.
func TestPendingLinkTimeoutCleansUpAndExpiresPath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(false) // Client / non-transport node

	iface := newAFI("test-iface", interfaces.ModeFull)
	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "hub-client")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// Seed path table
	ts.mu.Lock()
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Hops:      2,
		Interface: iface,
		Expires:   time.Now().Add(time.Hour),
		Timestamp: time.Now(),
	}
	ts.mu.Unlock()

	// Create and register a pending link
	link, err := NewLink(ts, dest)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	link.status.Store(LinkClosed)
	link.teardownReason = TeardownTimeout

	ts.RegisterLink(link)

	ts.mu.Lock()
	if len(ts.pendingLinks) != 1 {
		ts.mu.Unlock()
		t.Fatalf("pendingLinks count = %v, want 1", len(ts.pendingLinks))
	}
	ts.mu.Unlock()

	// Run processPendingLinks
	ts.processPendingLinks(time.Now())

	ts.mu.Lock()
	pendingLen := len(ts.pendingLinks)
	_, hasPath := ts.pathTable[string(dest.Hash)]
	_, hasPR := ts.pathRequests[string(dest.Hash)]
	ts.mu.Unlock()

	if pendingLen != 0 {
		t.Errorf("pendingLinks count after timeout = %v, want 0 (pruned)", pendingLen)
	}
	if hasPath {
		t.Errorf("path for %x still present in pathTable, want expired/deleted", dest.Hash)
	}
	if !hasPR {
		t.Errorf("path request for %x was not initiated, want rediscovery requested", dest.Hash)
	}
}

// TestPendingLinkTimeoutTransportNodeMarksUnresponsiveAndRequestsPath verifies
// that when a pending link times out on a transport-enabled node, processPendingLinks
// prunes the closed link, marks the path unresponsive, and triggers path rediscovery.
// Mirrors Python RNS/Transport.py:530-575 and 765-778.
func TestPendingLinkTimeoutTransportNodeMarksUnresponsiveAndRequestsPath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true) // Transport-enabled node

	iface := newAFI("test-iface", interfaces.ModeFull)
	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "hub-transport")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// Seed path table
	ts.mu.Lock()
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Hops:         2,
		Interface:    iface,
		Expires:      time.Now().Add(time.Hour),
		Timestamp:    time.Now(),
		Unresponsive: false,
	}
	ts.mu.Unlock()

	// Create and register a pending link
	link, err := NewLink(ts, dest)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	link.status.Store(LinkClosed)
	link.teardownReason = TeardownTimeout

	ts.RegisterLink(link)

	// Run processPendingLinks
	ts.processPendingLinks(time.Now())

	ts.mu.Lock()
	pendingLen := len(ts.pendingLinks)
	entry, hasPath := ts.pathTable[string(dest.Hash)]
	_, hasPR := ts.pathRequests[string(dest.Hash)]
	ts.mu.Unlock()

	if pendingLen != 0 {
		t.Errorf("pendingLinks count after timeout = %v, want 0 (pruned)", pendingLen)
	}
	if !hasPath {
		t.Fatal("pathTable entry was deleted; transport node should mark unresponsive instead")
	}
	if !entry.Unresponsive {
		t.Errorf("path for %x Unresponsive = false, want true", dest.Hash)
	}
	if !hasPR {
		t.Errorf("path request for %x was not initiated, want rediscovery requested", dest.Hash)
	}
}

// TestUnvalidatedLinkTableTimeoutMarksUnresponsiveAndRequestsPath verifies that
// when an unvalidated link entry in linkTable times out, cullStaleTransportTables
// removes the link entry, marks the path unresponsive, and triggers path rediscovery.
// Mirrors Python RNS/Transport.py:715-780.
func TestUnvalidatedLinkTableTimeoutMarksUnresponsiveAndRequestsPath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)

	iface := newAFI("test-iface", interfaces.ModeFull)
	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "hub-lt")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	// Seed path table
	ts.mu.Lock()
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Hops:         1,
		Interface:    iface,
		Expires:      time.Now().Add(time.Hour),
		Timestamp:    time.Now(),
		Unresponsive: false,
	}

	linkID := bytes.Repeat([]byte{0x42}, 16)
	ts.linkTable[string(linkID)] = &LinkEntry{
		DestinationHash: dest.Hash,
		Hops:            0,
		Validated:       false,
		ProofTimeout:    time.Now().Add(-1 * time.Second), // Timed out
		Timestamp:       time.Now().Add(-10 * time.Second),
	}
	ts.mu.Unlock()

	// Run cullStaleTransportTables
	ts.cullStaleTransportTables(time.Now())

	ts.mu.Lock()
	_, inLinkTable := ts.linkTable[string(linkID)]
	entry, hasPath := ts.pathTable[string(dest.Hash)]
	_, hasPR := ts.pathRequests[string(dest.Hash)]
	ts.mu.Unlock()

	if inLinkTable {
		t.Errorf("link %x still in linkTable, want culled", linkID)
	}
	if !hasPath {
		t.Fatal("pathTable entry was deleted; transport node should mark unresponsive")
	}
	if !entry.Unresponsive {
		t.Errorf("path for %x Unresponsive = false, want true", dest.Hash)
	}
	if !hasPR {
		t.Errorf("path request for %x was not initiated, want rediscovery requested", dest.Hash)
	}
}

// TestMultiHopLinkTimeoutMarksUnresponsiveAndRequestsPath pins the case the
// operator actually hit: a link request sent along a path of MORE THAN ONE hop
// that is never answered.
//
// The recovery block recognised four arrivals at a timed-out link request — the
// path is now missing; the link was originated by a local client (hops 0); the
// destination was previously one hop away; the link initiator is one hop away —
// and a multi-hop timeout matches none of them. The link entry was culled, and
// then nothing: the path stayed in the table, still marked responsive, so the
// next attempt selected the same next hop and paid another full establishment
// window. Observed on 2026-10-10 as a link request over a 2-hop path that got
// no answer for its whole 18 s timeout, while a fresh client fetched the same
// page over a 2-hop path in 529 ms and validated a link over a 4-hop path in
// 43 ms. Hop count was never the problem — the path was simply never
// invalidated, and the client could only find out by timing out.
//
// Python has the same four conditions (Transport.py:709-757) and its own
// comment admits the shape is a known dead end ("This might result in the path
// re-resolution only being able to happen once … Is this problematic, or does
// it actually not matter?"). It does matter: it is one full link-establishment
// timeout, every time, for a destination that is perfectly reachable by
// another route.
func TestMultiHopLinkTimeoutMarksUnresponsiveAndRequestsPath(t *testing.T) {
	t.Parallel()
	ts := NewTransportSystem(nil)
	ts.identity = mustTestNewIdentity(t, true)
	ts.SetEnabled(true)

	iface := newAFI("Local TCP Hub", interfaces.ModeFull)
	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(nil, id, DestinationIn, DestinationSingle, "hub-multihop")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}

	ts.mu.Lock()
	ts.pathTable[string(dest.Hash)] = &PathEntry{
		Hops:         2,
		Interface:    iface,
		Expires:      time.Now().Add(time.Hour),
		Timestamp:    time.Now(),
		Unresponsive: false,
	}

	linkID := bytes.Repeat([]byte{0x55}, 16)
	ts.linkTable[string(linkID)] = &LinkEntry{
		DestinationHash: dest.Hash,
		Hops:            2,
		RemainingHops:   2,
		Validated:       false,
		ProofTimeout:    time.Now().Add(-1 * time.Second), // Timed out
		Timestamp:       time.Now().Add(-10 * time.Second),
	}
	ts.mu.Unlock()

	ts.cullStaleTransportTables(time.Now())

	ts.mu.Lock()
	_, inLinkTable := ts.linkTable[string(linkID)]
	entry, hasPath := ts.pathTable[string(dest.Hash)]
	_, hasPR := ts.pathRequests[string(dest.Hash)]
	ts.mu.Unlock()

	if inLinkTable {
		t.Errorf("link %x still in linkTable, want culled", linkID)
	}
	if !hasPath {
		t.Fatal("pathTable entry was deleted; transport node should mark unresponsive")
	}
	if !entry.Unresponsive {
		t.Errorf("path for %x over a %v-hop route timed out and was left responsive, so the next attempt reuses it", dest.Hash, entry.Hops)
	}
	if !hasPR {
		t.Errorf("path request for %x was not initiated, want rediscovery requested", dest.Hash)
	}
}
