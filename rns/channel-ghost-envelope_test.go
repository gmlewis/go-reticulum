// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
	"time"
)

// ghostFilterOutlet is a ChannelOutlet whose GetPacketID enforces the
// raw!=nil guard (like the real LinkChannelOutlet), so a ghost envelope
// whose packet has no on-wire Raw is not matched in the txRing loops. Its
// Send returns a packet that carries Raw so the real (non-ghost) path is
// exercised too.
type ghostFilterOutlet struct {
	sends byte
}

func (o *ghostFilterOutlet) Send(raw []byte) (*Packet, error) {
	o.sends++
	return &Packet{PacketHash: []byte{o.sends}, Raw: raw, Receipt: &PacketReceipt{}}, nil
}

func (o *ghostFilterOutlet) Resend(p *Packet) (*Packet, error) { return p, nil }
func (o *ghostFilterOutlet) MDU() int                          { return 512 }
func (o *ghostFilterOutlet) RTT() float64                      { return 0.1 }
func (o *ghostFilterOutlet) IsUsable() bool                    { return true }
func (o *ghostFilterOutlet) TimedOut()                         {}

// GetPacketID mirrors LinkChannelOutlet.GetPacketID: nil for a packet with
// no on-wire Raw, else the packet hash.
func (o *ghostFilterOutlet) GetPacketID(p *Packet) []byte {
	if p == nil || len(p.Raw) == 0 {
		return nil
	}
	return p.PacketHash
}

// TestChannelGhostEnvelopeNotMatchedByDelivered verifies that a
// ghost envelope (a packet with a hash but no on-wire Raw, e.g. one whose
// outlet send produced no packet) must not be matched by the txRing delivery
// loop, because GetPacketID returns nil for it (Python Channel.py:418-420,
// 600-603). A real envelope with the same identity IS matched and removed.
func TestChannelGhostEnvelopeNotMatchedByDelivered(t *testing.T) {
	t.Parallel()
	outlet := &ghostFilterOutlet{}
	ch := NewChannel(outlet)

	// Emplace a ghost envelope directly: a packet with a hash but no Raw.
	ghostHash := []byte("ghost-hash-12")
	ghost := &Envelope{
		TS:       time.Now(),
		Sequence: 7,
		Packet:   &Packet{PacketHash: ghostHash, Receipt: &PacketReceipt{Hash: ghostHash}},
	}
	ch.mu.Lock()
	ch.txRing = append(ch.txRing, ghost)
	ch.mu.Unlock()

	// A delivery for the ghost's hash must NOT remove it: GetPacketID is nil
	// for the ghost (no Raw), so the match loop skips it.
	ch.packetDelivered(&PacketReceipt{Hash: ghostHash})

	ch.mu.Lock()
	remaining := len(ch.txRing)
	ch.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("ghost envelope was matched/removed (txRing len=%v, want 1)", remaining)
	}

	// Control: a real envelope (Send produces a packet WITH Raw) is matched
	// and removed for its hash.
	realEnv, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("real")})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	ch.packetDelivered(&PacketReceipt{Hash: realEnv.Packet.PacketHash})

	ch.mu.Lock()
	remaining = len(ch.txRing)
	ch.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("real envelope not matched (txRing len=%v, want 1: ghost only)", remaining)
	}
}

// TestChannelGhostEnvelopeNotMatchedByTimeout verifies the timeout path:
// a ghost envelope must not be matched by the txRing timeout
// loop (Python Channel.py:466), so a timeout for its hash is a no-op — no
// resend, no teardown, no tries increment.
func TestChannelGhostEnvelopeNotMatchedByTimeout(t *testing.T) {
	t.Parallel()
	outlet := &ghostFilterOutlet{}
	ch := NewChannel(outlet)
	ch.maxTries = 2

	ghostHash := []byte("ghost-to-1234")
	ghost := &Envelope{
		TS:       time.Now(),
		Sequence: 9,
		Tries:    ch.maxTries, // a non-guarded match would tear the channel down
		Packet:   &Packet{PacketHash: ghostHash, Receipt: &PacketReceipt{Hash: ghostHash}},
	}
	ch.mu.Lock()
	ch.txRing = append(ch.txRing, ghost)
	ch.mu.Unlock()

	// A timeout for the ghost's hash must be a no-op: GetPacketID is nil for
	// the ghost, so the match loop finds nothing and returns without
	// resending or tearing the channel down.
	ch.packetTimeout(&PacketReceipt{Hash: ghostHash})

	ch.mu.Lock()
	remaining := len(ch.txRing)
	tries := ghost.Tries
	ch.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("ghost envelope was matched/removed by timeout (txRing len=%v, want 1)", remaining)
	}
	if tries != ch.maxTries {
		t.Fatalf("ghost tries=%v, want %v (timeout must not mutate a ghost)", tries, ch.maxTries)
	}
}

// TestLinkChannelOutletGetPacketIDNilGuard directly asserts the
// LinkChannelOutlet.GetPacketID raw!=nil guard.
func TestLinkChannelOutletGetPacketIDNilGuard(t *testing.T) {
	t.Parallel()

	outlet := &LinkChannelOutlet{} // link nil; GetPacketID must not deref link

	if got := outlet.GetPacketID(nil); got != nil {
		t.Fatalf("GetPacketID(nil)=%x, want nil", got)
	}
	// A packet with no Raw is a ghost: nil id.
	if got := outlet.GetPacketID(&Packet{PacketHash: []byte("h")}); got != nil {
		t.Fatalf("GetPacketID(no-Raw)=%x, want nil", got)
	}
	// A packet with Raw returns its hash.
	hash := []byte("real-hash-1")
	if got := outlet.GetPacketID(&Packet{PacketHash: hash, Raw: []byte("payload")}); !bytes.Equal(got, hash) {
		t.Fatalf("GetPacketID(Raw)=%x, want %x", got, hash)
	}
}
