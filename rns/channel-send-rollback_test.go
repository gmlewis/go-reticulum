// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"errors"
	"sync"
	"testing"
)

// sendFailingOutlet is a ChannelOutlet whose Send behavior is controllable
// from a test so the rollback path in Channel.Send can be exercised: Send
// can return an error, a nil packet, or delegate to a custom function.
type sendFailingOutlet struct {
	mdu        int
	sendErr    error
	sendNilPkt bool
	sendFn     func([]byte) (*Packet, error)
	sends      int
}

func (o *sendFailingOutlet) Send(raw []byte) (*Packet, error) {
	o.sends++
	if o.sendFn != nil {
		return o.sendFn(raw)
	}
	if o.sendErr != nil {
		return nil, o.sendErr
	}
	if o.sendNilPkt {
		return nil, nil
	}
	return &Packet{PacketHash: []byte{byte(o.sends)}, Receipt: &PacketReceipt{}}, nil
}

func (o *sendFailingOutlet) Resend(p *Packet) (*Packet, error) { return p, nil }
func (o *sendFailingOutlet) MDU() int {
	if o.mdu > 0 {
		return o.mdu
	}
	return 512
}
func (o *sendFailingOutlet) RTT() float64   { return 0.1 }
func (o *sendFailingOutlet) IsUsable() bool { return true }
func (o *sendFailingOutlet) TimedOut()      {}

// GetPacketID returns the mock packet's hash; the mock packets carry no Raw.
func (o *sendFailingOutlet) GetPacketID(p *Packet) []byte {
	if p == nil {
		return nil
	}
	return p.PacketHash
}

// TestChannelSendRollsBackSequenceOnOutletError verifies that
// when the outlet send fails, Channel.Send must roll back the reserved
// sequence number (Python Channel.py:515-516) so it is not consumed, and the
// envelope must not be emplaced in the txRing.
func TestChannelSendRollsBackSequenceOnOutletError(t *testing.T) {
	t.Parallel()
	outlet := &sendFailingOutlet{mdu: 512, sendErr: errors.New("link down")}
	ch := NewChannel(outlet)
	before := ch.nextSequence

	_, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
	if err == nil {
		t.Fatal("Send succeeded, want error from failing outlet")
	}
	if ch.nextSequence != before {
		t.Fatalf("nextSequence=%v after failed send, want %v (rolled back)", ch.nextSequence, before)
	}
	if len(ch.txRing) != 0 {
		t.Fatalf("txRing len=%v after failed send, want 0 (envelope not emplaced)", len(ch.txRing))
	}
}

// TestChannelSendRollsBackSequenceOnNilPacket covers the second rollback
// condition: when the outlet returns a nil packet with no
// error, Send treats it as "did not transmit" and rolls back the sequence.
func TestChannelSendRollsBackSequenceOnNilPacket(t *testing.T) {
	t.Parallel()
	outlet := &sendFailingOutlet{mdu: 512, sendNilPkt: true}
	ch := NewChannel(outlet)
	before := ch.nextSequence

	_, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
	if err == nil {
		t.Fatal("Send succeeded, want error from nil-packet outlet")
	}
	if ch.nextSequence != before {
		t.Fatalf("nextSequence=%v after nil-packet send, want %v (rolled back)", ch.nextSequence, before)
	}
	if len(ch.txRing) != 0 {
		t.Fatalf("txRing len=%v after nil-packet send, want 0", len(ch.txRing))
	}
}

// TestChannelSendReusesSequenceAfterRollback asserts the rolled-back sequence
// is reused by the next successful send: a transient failure does not leave a
// gap in the sequence space (Python Channel.py:516 restores _next_sequence to
// reserved_sequence, so the next send reserves the same number again).
func TestChannelSendReusesSequenceAfterRollback(t *testing.T) {
	t.Parallel()
	outlet := &sendFailingOutlet{mdu: 512}
	ch := NewChannel(outlet)

	env1, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("a")})
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	if env1.Sequence != 0 {
		t.Fatalf("first send sequence=%v, want 0", env1.Sequence)
	}

	outlet.sendErr = errors.New("transient")
	if _, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("b")}); err == nil {
		t.Fatal("second send succeeded, want transient error")
	}
	outlet.sendErr = nil

	env3, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("c")})
	if err != nil {
		t.Fatalf("third send: %v", err)
	}
	if env3.Sequence != 1 {
		t.Fatalf("third send sequence=%v, want 1 (rolled-back sequence reused)", env3.Sequence)
	}
}

// TestChannelSendLockSerializesConcurrentSends confirms the sendLock
// serializes concurrent Send calls: each successful send
// gets a distinct, densely-packed sequence number with no gaps or duplicates,
// even under concurrent senders. Without the lock the reserve/commit window
// could race and produce duplicate or out-of-order sequences.
func TestChannelSendLockSerializesConcurrentSends(t *testing.T) {
	t.Parallel()
	outlet := &sendFailingOutlet{mdu: 512}
	ch := NewChannel(outlet)
	// Widen the window so all n concurrent sends fit without tripping the
	// isReadyToSend window check; the test is about sendLock serialization,
	// not flow control.
	ch.window = 64
	ch.windowMax = 64

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	envs := make([]*Envelope, n)
	errs := make([]error, n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			envs[i], errs[i] = ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
		}(i)
	}
	wg.Wait()

	seen := make(map[uint16]int)
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("send %d: %v", i, errs[i])
		}
		if envs[i] == nil {
			t.Fatalf("send %d: nil envelope", i)
		}
		if dup, ok := seen[envs[i].Sequence]; ok {
			t.Fatalf("duplicate sequence %v from sends %d and %d (sendLock did not serialize)", envs[i].Sequence, dup, i)
		}
		seen[envs[i].Sequence] = i
	}
	if len(seen) != n {
		t.Fatalf("distinct sequences=%v, want %v", len(seen), n)
	}
}
