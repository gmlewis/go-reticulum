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

func TestPacket(t *testing.T) {
	t.Parallel()
	id := mustTestNewIdentity(t, true)
	ts := NewTransportSystem(nil)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "testapp")

	data := []byte("hello reticulum")
	p := NewPacket(dest, data)

	if err := p.Pack(); err != nil {
		t.Fatal(err)
	}

	if !p.Packed {
		t.Errorf("packet not marked as packed")
	}

	if len(p.Raw) == 0 {
		t.Errorf("raw bytes are empty")
	}

	// Test unpacking
	p2 := NewPacketFromRaw(p.Raw)
	if err := p2.Unpack(); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(p.DestinationHash, p2.DestinationHash) {
		t.Errorf("destination hash mismatch")
	}

	if p.PacketType != p2.PacketType {
		t.Errorf("packet type mismatch")
	}

	if p.Context != p2.Context {
		t.Errorf("context mismatch")
	}

	// Test hash consistency
	if !bytes.Equal(p.PacketHash, p2.PacketHash) {
		t.Errorf("packet hash mismatch")
	}
}

func TestPacketEncryption(t *testing.T) {
	t.Parallel()
	id := mustTestNewIdentity(t, true)
	ts := NewTransportSystem(nil)
	dest := mustTestNewDestination(t, ts, id, DestinationIn, DestinationSingle, "testapp")

	data := []byte("secret message")
	p := NewPacket(dest, data)
	if err := p.Pack(); err != nil {
		t.Fatal(err)
	}

	// Unpack and decrypt
	p2 := NewPacketFromRaw(p.Raw)
	if err := p2.Unpack(); err != nil {
		t.Fatal(err)
	}

	decrypted, err := dest.Decrypt(p2.Data)
	mustTest(t, err)

	if !bytes.Equal(data, decrypted) {
		t.Errorf("decryption failed: expected %s, got %s", data, decrypted)
	}
}

func TestPacketResendTimeout(t *testing.T) {
	t.Parallel()

	ts := NewTransportSystem(nil)
	id := mustTestNewIdentity(t, true)
	dest, err := NewDestination(ts, id, DestinationIn, DestinationSingle, "test", "app")
	if err != nil {
		t.Fatalf("NewDestination: %v", err)
	}
	pkt := NewPacket(dest, []byte("hello"))

	// Initially, the packet has not been sent.
	if pkt.GetStatus() != PacketStatusNone {
		t.Fatalf("initial packet status = %v, want PacketStatusNone", pkt.GetStatus())
	}

	// Manually mark the packet as sent, then check timeout.
	pkt.SentAt = float64(time.Now().Add(-2*time.Hour).UnixNano()) / 1e9
	pkt.timeout = 3600.0 // 1h

	if !pkt.IsTimedOut() {
		t.Fatal("packet should be timed out after 2h with 1h timeout")
	}

	// Resend resets the timeout clock and bumps the resent count.
	pkt.Resend()
	if !pkt.resent {
		t.Fatal("Resend did not flip the resent flag")
	}
	if pkt.GetStatus() != PacketStatusSent {
		t.Fatalf("packet status after Resend = %v, want PacketStatusSent", pkt.GetStatus())
	}
	if pkt.IsTimedOut() {
		t.Fatal("packet should not be timed out immediately after Resend")
	}
}
