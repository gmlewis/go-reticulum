// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rns/msgpack"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestCleanAvailableTicketsGolden is the Phase K golden test. It seeds the
// ticket store with a mix of fresh / expired / exact-boundary outbound and
// inbound tickets and asserts Router.CleanAvailableTickets reaps exactly the
// set Python's LXMRouter.clean_available_tickets (LXMRouter.py:1247-1269)
// reaps, and no more.
//
// The reaping rule (captured from the Python source and confirmed by a live
// Python run) is a STRICT comparison against time.time():
//
//   - outbound: reap when now > expires        (so expires == now is KEPT)
//   - inbound:  reap when now > expires + GRACE (so expires+GRACE == now is KEPT)
//
// where GRACE = LXMessage.TICKET_GRACE = 5*24*60*60 = 432000 = DefaultTicketGraceSeconds.
//
// The live Python capture confirmed the non-boundary cases:
//
//	outbound: fresh (now+3600) kept, expired (now-3600) reaped.
//	inbound:  fresh (now+3600) kept, within-grace (now-100) kept,
//	          beyond-grace (now-GRACE-3600) reaped.
//
// The exact-boundary cases (expires == now; expires+GRACE == now) are
// deterministic only with a fixed clock, so they are asserted here against the
// strict-`>` semantics read from the Python source.
func TestCleanAvailableTicketsGolden(t *testing.T) {
	t.Parallel()

	// Fixed clock so the exact-boundary cases are deterministic.
	now := time.Unix(1_700_000_000, 0).UTC()
	ts := rns.NewTransportSystem(nil)
	router := mustTestNewRouter(t, ts, nil, testutils.TempDir(t, tempDirPrefix))
	router.now = func() time.Time { return now }

	nowSec := float64(now.UnixNano()) / 1e9
	grace := float64(DefaultTicketGraceSeconds)
	ticket := bytes.Repeat([]byte{0x01}, TicketLength)

	// --- Outbound (keyed by destination hash) ---
	// Three distinct destination hashes, one per case.
	freshDest := bytes.Repeat([]byte{0xA1}, DestinationLength)
	expiredDest := bytes.Repeat([]byte{0xA2}, DestinationLength)
	boundaryDest := bytes.Repeat([]byte{0xA3}, DestinationLength)

	router.ticketStore.mu.Lock()
	router.ticketStore.outbound[string(freshDest)] = TicketEntry{
		Expires: nowSec + 3600, Ticket: ticket, // fresh -> keep
	}
	router.ticketStore.outbound[string(expiredDest)] = TicketEntry{
		Expires: nowSec - 3600, Ticket: ticket, // expired (now > expires) -> reap
	}
	router.ticketStore.outbound[string(boundaryDest)] = TicketEntry{
		Expires: nowSec, Ticket: ticket, // now > expires is false -> keep
	}

	// --- Inbound (keyed by destination hash -> ticket -> entry) ---
	dest := bytes.Repeat([]byte{0xBB}, DestinationLength)
	router.ticketStore.inbound[string(dest)] = map[string]TicketEntry{}
	inboundCases := []struct {
		label    string
		expires  float64
		survives bool
	}{
		{"fresh", nowSec + 3600, true},                 // keep
		{"within_grace", nowSec - 100, true},           // now < exp+grace -> keep
		{"beyond_grace", nowSec - grace - 3600, false}, // now > exp+grace -> reap
		{"boundary", nowSec - grace, true},             // now > exp+grace false -> keep
	}
	inboundTickets := make(map[string][]byte, len(inboundCases))
	for i, c := range inboundCases {
		tk := bytes.Repeat([]byte{byte(0x30 + i)}, TicketLength)
		inboundTickets[c.label] = tk
		router.ticketStore.inbound[string(dest)][string(tk)] = TicketEntry{
			Expires: c.expires, Ticket: tk,
		}
	}
	router.ticketStore.mu.Unlock()

	router.CleanAvailableTickets()

	// Outbound survivors.
	router.ticketStore.mu.RLock()
	_, freshOK := router.ticketStore.outbound[string(freshDest)]
	_, expiredOK := router.ticketStore.outbound[string(expiredDest)]
	_, boundaryOK := router.ticketStore.outbound[string(boundaryDest)]
	inbound := router.ticketStore.inbound[string(dest)]
	router.ticketStore.mu.RUnlock()

	if !freshOK {
		t.Error("outbound fresh ticket was reaped, want kept")
	}
	if expiredOK {
		t.Error("outbound expired ticket was kept, want reaped")
	}
	if !boundaryOK {
		t.Error("outbound exact-boundary (expires==now) ticket was reaped, want kept (strict >)")
	}

	// Inbound survivors.
	for _, c := range inboundCases {
		_, present := inbound[string(inboundTickets[c.label])]
		if c.survives && !present {
			t.Errorf("inbound %s ticket was reaped, want kept", c.label)
		}
		if !c.survives && present {
			t.Errorf("inbound %s ticket was kept, want reaped", c.label)
		}
	}
}

// TestLoadAvailableTicketsRunsCleanAvailableTickets verifies that loading
// tickets from disk invokes the cleaner (mirroring Python line 283:
// self.clean_available_tickets() right after load), so an expired outbound
// ticket and an expired-beyond-grace inbound ticket are reaped on load while
// fresh and within-grace entries survive.
func TestLoadAvailableTicketsRunsCleanAvailableTickets(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	ts := rns.NewTransportSystem(nil)
	tmpDir := testutils.TempDir(t, tempDirPrefix)
	router := mustTestNewRouter(t, ts, nil, tmpDir)
	router.now = func() time.Time { return now }

	nowSec := float64(now.UnixNano()) / 1e9
	grace := float64(DefaultTicketGraceSeconds)
	ticket := bytes.Repeat([]byte{0x07}, TicketLength)
	dest := bytes.Repeat([]byte{0xCC}, DestinationLength)

	// Seed the on-disk available_tickets file with a fresh and an expired
	// outbound ticket, and a within-grace and a beyond-grace inbound ticket,
	// mirroring the Python persisted layout.
	outbound := map[string]any{
		string(bytes.Repeat([]byte{0xD1}, DestinationLength)): []any{nowSec + 3600, ticket}, // fresh
		string(bytes.Repeat([]byte{0xD2}, DestinationLength)): []any{nowSec - 3600, ticket}, // expired
	}
	inboundDest := map[string]any{
		string(bytes.Repeat([]byte{0x10}, TicketLength)): []any{nowSec - 100},          // within grace
		string(bytes.Repeat([]byte{0x20}, TicketLength)): []any{nowSec - grace - 3600}, // beyond grace
	}
	inbound := map[string]any{string(dest): inboundDest}
	payload := map[string]any{
		"outbound":        outbound,
		"inbound":         inbound,
		"last_deliveries": map[string]any{},
	}
	packed, err := msgpack.Pack(payload)
	if err != nil {
		t.Fatalf("pack payload: %v", err)
	}
	if err := os.WriteFile(router.availableTicketsPath(), packed, 0o644); err != nil {
		t.Fatalf("write available_tickets file: %v", err)
	}

	// Replace the in-memory store and reload from disk.
	router.ticketStore = NewTicketStore()
	if err := router.LoadAvailableTickets(); err != nil {
		t.Fatalf("LoadAvailableTickets: %v", err)
	}

	router.ticketStore.mu.RLock()
	_, freshOK := router.ticketStore.outbound[string(bytes.Repeat([]byte{0xD1}, DestinationLength))]
	_, expiredOK := router.ticketStore.outbound[string(bytes.Repeat([]byte{0xD2}, DestinationLength))]
	gotInbound := router.ticketStore.inbound[string(dest)]
	router.ticketStore.mu.RUnlock()

	if !freshOK {
		t.Error("fresh outbound ticket was reaped on load, want kept")
	}
	if expiredOK {
		t.Error("expired outbound ticket was kept on load, want reaped")
	}
	if _, ok := gotInbound[string(bytes.Repeat([]byte{0x10}, TicketLength))]; !ok {
		t.Error("within-grace inbound ticket was reaped on load, want kept")
	}
	if _, ok := gotInbound[string(bytes.Repeat([]byte{0x20}, TicketLength))]; ok {
		t.Error("beyond-grace inbound ticket was kept on load, want reaped")
	}
}
