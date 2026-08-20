// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// TestChannelShutdownMarksEnvelopesUntracked verifies that
// Shutdown must clear the `tracked` flag on every envelope in both rings
// before clearing the rings (Python Channel._clear_rings, Channel.py:313-320),
// so a stale reference to an envelope can detect it is no longer owned by a
// ring. The txRing envelope comes from a real Send (tracked, with a packet
// and armed receipt callbacks); the rxRing envelope is emplaced directly.
func TestChannelShutdownMarksEnvelopesUntracked(t *testing.T) {
	t.Parallel()
	outlet := &sendFailingOutlet{mdu: 512}
	ch := NewChannel(outlet)

	txEnv, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !txEnv.tracked {
		t.Fatalf("txEnv.tracked=false after Send, want true (emplaceEnvelope marks tracked)")
	}

	// Emplace an rxRing envelope directly (e.g. an out-of-order receive).
	rxEnv := &Envelope{TS: time.Now(), Sequence: 100, Raw: []byte("rx")}
	ch.mu.Lock()
	if !ch.emplaceEnvelope(rxEnv, &ch.rxRing) {
		ch.mu.Unlock()
		t.Fatalf("emplaceEnvelope(rxRing) returned false")
	}
	ch.mu.Unlock()
	if !rxEnv.tracked {
		t.Fatalf("rxEnv.tracked=false after emplace, want true")
	}

	ch.Shutdown()

	if txEnv.tracked {
		t.Fatalf("txEnv.tracked=true after Shutdown, want false (clearRings must untrack)")
	}
	if rxEnv.tracked {
		t.Fatalf("rxEnv.tracked=true after Shutdown, want false (clearRings must untrack)")
	}
	ch.mu.Lock()
	txLen, rxLen := len(ch.txRing), len(ch.rxRing)
	ch.mu.Unlock()
	if txLen != 0 || rxLen != 0 {
		t.Fatalf("rings not cleared: tx=%v rx=%v", txLen, rxLen)
	}
}

// TestChannelShutdownNoUseAfterShutdown covers the safety
// guarantee: after Shutdown clears the rings and disarms the receipt
// callbacks, a late delivery or timeout callback (simulated by calling the
// channel methods directly with the stale receipt) must be a no-op — no
// panic, no re-entrance into a cleared ring, and the envelope stays untracked.
func TestChannelShutdownNoUseAfterShutdown(t *testing.T) {
	t.Parallel()
	outlet := &sendFailingOutlet{mdu: 512}
	ch := NewChannel(outlet)

	env, err := ch.Send(&StreamDataMessage{StreamID: 1, Data: []byte("x")})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	receipt := env.Packet.Receipt

	ch.Shutdown()

	// Late callbacks must be no-ops: the rings are cleared and the receipt's
	// channel callbacks were disarmed in clearRings, so neither path re-enters
	// the channel state.
	ch.packetDelivered(receipt)
	ch.packetTimeout(receipt)

	ch.mu.Lock()
	txLen := len(ch.txRing)
	ch.mu.Unlock()
	if txLen != 0 {
		t.Fatalf("txRing len=%v after late callbacks, want 0 (use after shutdown)", txLen)
	}
	if env.tracked {
		t.Fatalf("env.tracked=true after late callbacks, want false")
	}
}
