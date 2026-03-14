// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"crypto/rand"
	"sync"
	"time"
)

// TicketEntry structures the metadata for an authorized delivery ticket, pairing the raw cryptographic material with its absolute expiration timestamp.
type TicketEntry struct {
	Expires float64
	Ticket  []byte
}

// TicketStore manages the thread-safe persistence and lifecycle of delivery tickets, segregating inbound access grants from outbound delivery authorizations.
type TicketStore struct {
	mu             sync.RWMutex
	lastDeliveries map[string]float64
	inbound        map[string]map[string]TicketEntry
	outbound       map[string]TicketEntry
}

// NewTicketStore initializes an empty ticket storage subsystem, ready to track and issue delivery authorizations for the LXMF routing layer.
func NewTicketStore() *TicketStore {
	return &TicketStore{
		lastDeliveries: map[string]float64{},
		inbound:        map[string]map[string]TicketEntry{},
		outbound:       map[string]TicketEntry{},
	}
}

// MarkDelivery records the precise timestamp of a successful delivery to the specified destination hash, ensuring subsequent ticket generation obeys required intervals.
func (s *TicketStore) MarkDelivery(destinationHash []byte, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastDeliveries[string(destinationHash)] = float64(at.UnixNano()) / 1e9
}

// GenerateInboundTicket provisions a novel delivery authorization ticket for the specified destination, observing required renewal limits and interval thresholds.
func (s *TicketStore) GenerateInboundTicket(destinationHash []byte, now time.Time, expirySeconds float64) *TicketEntry {
	nowSeconds := float64(now.UnixNano()) / 1e9
	if expirySeconds <= 0 {
		expirySeconds = DefaultTicketExpirySeconds
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	destinationKey := string(destinationHash)

	if lastDelivery, ok := s.lastDeliveries[destinationKey]; ok {
		if nowSeconds-lastDelivery < DefaultTicketIntervalSeconds {
			return nil
		}
	}

	if existing, ok := s.inbound[destinationKey]; ok {
		for _, entry := range existing {
			if entry.Expires-nowSeconds > DefaultTicketRenewSeconds {
				copyEntry := TicketEntry{Expires: entry.Expires, Ticket: cloneBytes(entry.Ticket)}
				return &copyEntry
			}
		}
	} else {
		s.inbound[destinationKey] = map[string]TicketEntry{}
	}

	ticket := make([]byte, TicketLength)
	if _, err := rand.Read(ticket); err != nil {
		return nil
	}

	entry := TicketEntry{
		Expires: nowSeconds + expirySeconds,
		Ticket:  ticket,
	}

	s.inbound[destinationKey][string(ticket)] = entry
	copyEntry := TicketEntry{Expires: entry.Expires, Ticket: cloneBytes(entry.Ticket)}
	return &copyEntry
}

// RememberOutboundTicket securely caches a delivery ticket received from a peer, reserving it for future outbound message delivery to that specific destination.
func (s *TicketStore) RememberOutboundTicket(destinationHash []byte, entry TicketEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outbound[string(destinationHash)] = TicketEntry{Expires: entry.Expires, Ticket: cloneBytes(entry.Ticket)}
}

// OutboundTicket retrieves a cached outbound delivery ticket for the specified destination, provided the ticket has not yet eclipsed its absolute expiration time.
func (s *TicketStore) OutboundTicket(destinationHash []byte, now time.Time) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.outbound[string(destinationHash)]
	if !ok {
		return nil
	}
	nowSeconds := float64(now.UnixNano()) / 1e9
	if entry.Expires <= nowSeconds {
		return nil
	}

	return cloneBytes(entry.Ticket)
}

// OutboundTicketExpiry evaluates the remaining lifespan of a cached outbound ticket, returning the raw expiration timestamp or zero if invalid.
func (s *TicketStore) OutboundTicketExpiry(destinationHash []byte, now time.Time) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.outbound[string(destinationHash)]
	if !ok {
		return 0
	}
	nowSeconds := float64(now.UnixNano()) / 1e9
	if entry.Expires <= nowSeconds {
		return 0
	}

	return entry.Expires
}

// InboundTickets compiles a slice of all currently valid inbound delivery tickets allocated to a given destination hash, filtering out any expired entries.
func (s *TicketStore) InboundTickets(destinationHash []byte, now time.Time) [][]byte {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, ok := s.inbound[string(destinationHash)]
	if !ok {
		return nil
	}
	nowSeconds := float64(now.UnixNano()) / 1e9

	result := make([][]byte, 0, len(entries))
	for _, entry := range entries {
		if entry.Expires > nowSeconds {
			result = append(result, cloneBytes(entry.Ticket))
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}
