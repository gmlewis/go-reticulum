// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can found in the LICENSE file.

package rns

import (
	"sync"
	"testing"
)

// timeoutReentrancyOutlet is a ChannelOutlet that records TimedOut calls and
// can mark a packet's receipt delivered during Resend, so the timeout
// delivered-guard and post-resend delivered check can be exercised.
type timeoutReentrancyOutlet struct {
	mdu             int
	sends           byte
	timedOutCalls   int
	mu              sync.Mutex
	deliverOnResend bool
}

func (o *timeoutReentrancyOutlet) Send(raw []byte) (*Packet, error) {
	o.sends++
	return &Packet{PacketHash: []byte{o.sends}, Receipt: &PacketReceipt{}}, nil
}

func (o *timeoutReentrancyOutlet) Resend(p *Packet) (*Packet, error) {
	if o.deliverOnResend && p != nil && p.Receipt != nil {
		p.Receipt.mu.Lock()
		p.Receipt.Proved = true
		p.Receipt.Status = ReceiptDelivered
		p.Receipt.mu.Unlock()
	}
	return p, nil
}

func (o *timeoutReentrancyOutlet) MDU() int {
	if o.mdu > 0 {
		return o.mdu
	}
	return 512
}
func (o *timeoutReentrancyOutlet) RTT() float64   { return 0.1 }
func (o *timeoutReentrancyOutlet) IsUsable() bool { return true }
func (o *timeoutReentrancyOutlet) TimedOut() {
	o.mu.Lock()
	o.timedOutCalls++
	o.mu.Unlock()
}

// GetPacketID returns the mock packet's hash; the mock packets carry no Raw.
func (o *timeoutReentrancyOutlet) GetPacketID(p *Packet) []byte {
	if p == nil {
		return nil
	}
	return p.PacketHash
}

// TestChannelPacketTimeoutEarlyReturnOnDelivered verifies that when
// a packet is delivered concurrently with its timeout, packetTimeout must
// early-return (Python Channel.py:461) instead of resending or tearing the
// channel down. The packet's receipt is marked delivered and tries is pinned
// at maxTries so a non-guarded timeout would call TimedOut/Shutdown; the guard
// must suppress both.
func TestChannelPacketTimeoutEarlyReturnOnDelivered(t *testing.T) {
	t.Parallel()
	outlet := &timeoutReentrancyOutlet{mdu: 512}
	ch := NewChannel(outlet)
	ch.maxTries = 3

	env, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Simulate the packet being delivered before the timeout fires.
	env.Packet.Receipt.mu.Lock()
	env.Packet.Receipt.Proved = true
	env.Packet.Receipt.Status = ReceiptDelivered
	env.Packet.Receipt.mu.Unlock()
	// Pin tries at maxTries so a non-guarded path would tear the channel down.
	env.Tries = ch.maxTries

	ch.packetTimeout(env.Packet.Receipt)

	if got := outlet.timedOutCalls; got != 0 {
		t.Fatalf("TimedOut called %v times, want 0 (packet already delivered)", got)
	}
	if env.Tries != ch.maxTries {
		t.Fatalf("env.Tries=%v after delivered timeout, want %v (no increment)", env.Tries, ch.maxTries)
	}
	if len(ch.txRing) != 1 {
		t.Fatalf("txRing len=%v, want 1 (no teardown/cleanup on delivered early-return)", len(ch.txRing))
	}
}

// TestChannelPacketTimeoutPostResendDeliveredCheck covers the
// post-resend delivered check (Python Channel.py:523-524): if the packet is
// delivered while the resend is in flight, packetTimeout must run the
// delivered path so the envelope leaves the txRing and the window grows,
// rather than leaving a delivered envelope pending in the ring.
func TestChannelPacketTimeoutPostResendDeliveredCheck(t *testing.T) {
	t.Parallel()
	outlet := &timeoutReentrancyOutlet{mdu: 512, deliverOnResend: true}
	ch := NewChannel(outlet)
	ch.maxTries = 5

	env, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(ch.txRing) != 1 {
		t.Fatalf("txRing len=%v after send, want 1", len(ch.txRing))
	}

	ch.packetTimeout(env.Packet.Receipt)

	// The resend marked the receipt delivered; the post-resend check must
	// run packetDelivered, removing the envelope from the ring. (The net
	// window change is zero — the timeout decremented before resend and
	// the delivery incremented after — so the ring cleanup, not the window,
	// is the observable that the delivered path ran.)
	if len(ch.txRing) != 0 {
		t.Fatalf("txRing len=%v after delivered resend, want 0 (envelope not cleaned up)", len(ch.txRing))
	}
	if env.Packet.Receipt.Status != ReceiptDelivered {
		t.Fatalf("receipt status=%v, want ReceiptDelivered", env.Packet.Receipt.Status)
	}
	if outlet.timedOutCalls != 0 {
		t.Fatalf("TimedOut called %v times, want 0", outlet.timedOutCalls)
	}
}

// TestChannelPacketTimeoutNoDoubleTeardownOnRacedDelivery asserts the
// delivered-guard prevents a double teardown: a packet at the teardown
// threshold that is delivered before the timeout fires must not trigger
// TimedOut, and a second timeout on the (now-delivered) envelope must also be
// a no-op rather than tearing the link down a second time.
func TestChannelPacketTimeoutNoDoubleTeardownOnRacedDelivery(t *testing.T) {
	t.Parallel()
	outlet := &timeoutReentrancyOutlet{mdu: 512}
	ch := NewChannel(outlet)
	ch.maxTries = 2

	env, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	env.Tries = ch.maxTries

	// First timeout at maxTries tears the channel down (no delivery yet).
	ch.packetTimeout(env.Packet.Receipt)
	if got := outlet.timedOutCalls; got != 1 {
		t.Fatalf("TimedOut called %v times after first timeout, want 1", got)
	}

	// Now the packet is delivered; a second timeout must NOT tear down again.
	env.Packet.Receipt.mu.Lock()
	env.Packet.Receipt.Proved = true
	env.Packet.Receipt.Status = ReceiptDelivered
	env.Packet.Receipt.mu.Unlock()

	ch.packetTimeout(env.Packet.Receipt)
	if got := outlet.timedOutCalls; got != 1 {
		t.Fatalf("TimedOut called %v times after delivered timeout, want 1 (no double teardown)", got)
	}
}
