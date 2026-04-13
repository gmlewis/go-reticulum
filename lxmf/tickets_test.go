// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"
	"time"
)

func TestTicketStoreGenerateAndReuse(t *testing.T) {
	t.Parallel()
	store := NewTicketStore()
	destinationHash := []byte("destination-hash-01")
	now := time.Unix(1700000000, 0)

	entry := store.GenerateInboundTicket(destinationHash, now, DefaultTicketExpirySeconds)
	if entry == nil {
		t.Fatal("expected generated inbound ticket")
	}
	if len(entry.Ticket) != TicketLength {
		t.Fatalf("ticket length=%v want=%v", len(entry.Ticket), TicketLength)
	}

	entry2 := store.GenerateInboundTicket(destinationHash, now.Add(2*time.Hour), DefaultTicketExpirySeconds)
	if entry2 == nil {
		t.Fatal("expected inbound ticket reuse")
	}
	if string(entry.Ticket) != string(entry2.Ticket) {
		t.Fatal("expected existing valid inbound ticket to be reused")
	}
}

func TestTicketStoreDeliveryIntervalGate(t *testing.T) {
	t.Parallel()
	store := NewTicketStore()
	destinationHash := []byte("destination-hash-02")
	now := time.Unix(1700000000, 0)

	store.MarkDelivery(destinationHash, now)
	entry := store.GenerateInboundTicket(destinationHash, now.Add(time.Hour), DefaultTicketExpirySeconds)
	if entry != nil {
		t.Fatal("expected no ticket generation within ticket interval gate")
	}
}

func TestTicketStoreOutboundAndInboundQueries(t *testing.T) {
	t.Parallel()
	store := NewTicketStore()
	destinationHash := []byte("destination-hash-03")
	now := time.Unix(1700000000, 0)

	entry := TicketEntry{
		Expires: float64(now.Add(2 * time.Hour).Unix()),
		Ticket:  []byte("ticket-ticket-123"),
	}
	store.RememberOutboundTicket(destinationHash, entry)

	if got := store.OutboundTicket(destinationHash, now); string(got) != string(entry.Ticket) {
		t.Fatalf("outbound ticket mismatch: got %q want %q", string(got), string(entry.Ticket))
	}
	if expiry := store.OutboundTicketExpiry(destinationHash, now); expiry != entry.Expires {
		t.Fatalf("outbound expiry mismatch: got %f want %f", expiry, entry.Expires)
	}

	inboundEntry := store.GenerateInboundTicket(destinationHash, now, DefaultTicketExpirySeconds)
	if inboundEntry == nil {
		t.Fatal("expected inbound ticket generation")
	}
	tickets := store.InboundTickets(destinationHash, now)
	if len(tickets) == 0 {
		t.Fatal("expected at least one active inbound ticket")
	}
}
