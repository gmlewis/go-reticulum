// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
)

func TestPacket(t *testing.T) {
	id := mustTestNewIdentity(t, true)
	ts := NewTransportSystem()
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
	id := mustTestNewIdentity(t, true)
	ts := NewTransportSystem()
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
