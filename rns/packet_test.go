// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"strings"
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

// TestPacketUnpackRejectsExcessiveHops verifies the hop-count validation in
// Packet.Unpack (RNS/Packet.py:245-248, v1.3.8): a packet whose hops byte is
// >= PathfinderM (128) is rejected with an error mentioning the hop count,
// while hops at or below 127 unpack normally. The transport read loop already
// logs and continues on Unpack error, so this guards against forwarding packets
// with an exhausted/attacked hop count.
func TestPacketUnpackRejectsExcessiveHops(t *testing.T) {
	t.Parallel()
	// Minimal Header1 raw packet: [flags][hops][dsthash(16)][context] = 19 bytes.
	buildRaw := func(hops byte) []byte {
		raw := make([]byte, 2+16+1)
		raw[0] = 0x00 // flags: Header1, all type fields zero
		raw[1] = hops
		// dsthash (raw[2:18]) and context (raw[18]) left zero
		return raw
	}
	cases := []struct {
		name    string
		hops    byte
		wantErr bool
	}{
		{"zero hops", 0x00, false},
		{"max valid hops 127", 0x7F, false},
		{"pathfinder M 128", 0x80, true},
		{"far over 255", 0xFF, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := NewPacketFromRaw(buildRaw(tc.hops))
			err := p.Unpack()
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("Unpack with hops=%d expected error, got nil", tc.hops)
			case !tc.wantErr && err != nil:
				t.Fatalf("Unpack with hops=%d unexpected error: %v", tc.hops, err)
			case tc.wantErr && err != nil && !strings.Contains(err.Error(), "hop count"):
				t.Fatalf("error %q should mention hop count", err.Error())
			}
		})
	}
}
