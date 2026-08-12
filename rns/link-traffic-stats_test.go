// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"testing"
	"time"
)

// establishLoopbackLinkPair wires two TransportSystems together with a pipe,
// registers an IN destination on the receiver, and establishes a link from the
// initiator to it. It returns the active initiator link, the receiver-side link,
// and the receiver destination. It is the two-link variant of
// establishLoopbackLink (which only returns the initiator + destination) so
// traffic-counter tests can assert on the receiver's rx counters too.
func establishLoopbackLinkPair(t *testing.T) (initiator, receiver *Link, receiverDest *Destination) {
	t.Helper()
	tsInitiator := newTestTransportSystem(t)
	tsReceiver := newTestTransportSystem(t)

	pipeInitiator, pipeReceiver, cleanup := newTestPipes(t, tsInitiator, tsReceiver)
	t.Cleanup(cleanup)
	tsInitiator.RegisterInterface(pipeInitiator)
	tsReceiver.RegisterInterface(pipeReceiver)

	receiverDest = mustTestNewDestination(t, tsReceiver, tsReceiver.identity, DestinationIn, DestinationSingle, "receiver")

	establishedReceiver := make(chan *Link, 1)
	receiverDest.callbacks.LinkEstablished = func(l *Link) {
		establishedReceiver <- l
	}

	initiator = mustTestNewLink(t, tsInitiator, receiverDest)
	t.Cleanup(initiator.Teardown)

	establishedInitiator := make(chan bool, 1)
	initiator.callbacks.LinkEstablished = func(l *Link) {
		establishedInitiator <- true
	}

	if err := initiator.Establish(); err != nil {
		t.Fatalf("Establish: %v", err)
	}
	select {
	case <-establishedInitiator:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for initiator link establishment")
	}
	select {
	case l := <-establishedReceiver:
		t.Cleanup(l.Teardown)
		if l.status.Load() != LinkActive {
			t.Fatalf("receiver link not active: %v", l.status.Load())
		}
		receiver = l
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for receiver link establishment")
	}
	if initiator.status.Load() != LinkActive {
		t.Fatalf("initiator link not active: %v", initiator.status.Load())
	}
	return initiator, receiver, receiverDest
}

// TestLinkTrafficCountersZero covers the zero-value contract: a freshly
// constructed link reports zero on every traffic counter, matching Python
// Link.__init__ which sets self.tx = self.rx = self.txbytes = self.rxbytes = 0.
func TestLinkTrafficCountersZero(t *testing.T) {
	t.Parallel()

	link := mustTestNewLink(t, newTestTransportSystem(t), mustTestNewDestination(
		t, newTestTransportSystem(t), mustTestNewIdentity(t, true),
		DestinationIn, DestinationSingle, "d"))
	if got := link.GetTX(); got != 0 {
		t.Fatalf("GetTX() = %d, want 0", got)
	}
	if got := link.GetRX(); got != 0 {
		t.Fatalf("GetRX() = %d, want 0", got)
	}
	if got := link.GetTXBytes(); got != 0 {
		t.Fatalf("GetTXBytes() = %d, want 0", got)
	}
	if got := link.GetRXBytes(); got != 0 {
		t.Fatalf("GetRXBytes() = %d, want 0", got)
	}
}

// TestLinkTrafficCountersWired asserts that link-traffic counters increment
// during a real handshake and a subsequent outbound send, with txbytes
// reflecting the ciphertext length. It is the Go port of Python Link.py
// (self.tx += 1; self.txbytes += len(self.ciphertext) in Packet.send for a
// LINK destination, and self.rx += 1; self.rxbytes += len(packet.data) in
// Link.receive). The ciphertext length is deterministic for a given plaintext
// (Token.Encrypt = 16-byte IV + PKCS7-padded CBC + 32-byte HMAC), so a manual
// initiator.Encrypt(plaintext) yields the exact ciphertext length the packet
// carry on the wire.
func TestLinkTrafficCountersWired(t *testing.T) {
	t.Parallel()

	initiator, receiver, _ := establishLoopbackLinkPair(t)

	// After establishment the handshake (link request, proof, RTT) must
	// have bumped every counter on both ends.
	if got := initiator.GetTX(); got == 0 {
		t.Fatal("initiator GetTX() = 0 after handshake, want > 0")
	}
	if got := initiator.GetRX(); got == 0 {
		t.Fatal("initiator GetRX() = 0 after handshake, want > 0")
	}
	if got := initiator.GetTXBytes(); got == 0 {
		t.Fatal("initiator GetTXBytes() = 0 after handshake, want > 0")
	}
	if got := initiator.GetRXBytes(); got == 0 {
		t.Fatal("initiator GetRXBytes() = 0 after handshake, want > 0")
	}
	if got := receiver.GetTX(); got == 0 {
		t.Fatal("receiver GetTX() = 0 after handshake, want > 0")
	}
	if got := receiver.GetRX(); got == 0 {
		t.Fatal("receiver GetRX() = 0 after handshake, want > 0")
	}

	// Send exactly one data packet over the established link and verify
	// the outbound counter reflects the ciphertext length precisely.
	plaintext := []byte("link traffic counter golden payload")
	wantCiphertextLen, err := initiator.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("initiator.Encrypt: %v", err)
	}
	wantCT := len(wantCiphertextLen)

	initTX := initiator.GetTX()
	initTXBytes := initiator.GetTXBytes()
	recvRX := receiver.GetRX()
	recvRXBytes := receiver.GetRXBytes()

	p := NewPacket(initiator, plaintext)
	if err := p.Send(); err != nil {
		t.Fatalf("Packet.Send: %v", err)
	}

	// Outbound: tx += 1, txbytes += len(ciphertext).
	if got := initiator.GetTX(); got != initTX+1 {
		t.Fatalf("initiator GetTX() = %d, want %d", got, initTX+1)
	}
	if got := initiator.GetTXBytes(); got != initTXBytes+uint64(wantCT) {
		t.Fatalf("initiator GetTXBytes() = %d, want %d (=%d+%d)",
			got, initTXBytes+uint64(wantCT), initTXBytes, wantCT)
	}

	// Inbound on the receiver: rx += 1, rxbytes += len(ciphertext). The
	// ciphertext is unchanged in transit, so the length matches exactly.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if receiver.GetRX() >= recvRX+1 && receiver.GetRXBytes() >= recvRXBytes+uint64(wantCT) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := receiver.GetRX(); got != recvRX+1 {
		t.Fatalf("receiver GetRX() = %d, want %d", got, recvRX+1)
	}
	if got := receiver.GetRXBytes(); got != recvRXBytes+uint64(wantCT) {
		t.Fatalf("receiver GetRXBytes() = %d, want %d (=%d+%d)",
			got, recvRXBytes+uint64(wantCT), recvRXBytes, wantCT)
	}
}
