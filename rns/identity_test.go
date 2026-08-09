// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
)

type proofCaptureTransport struct {
	*TransportSystem
	lastPacket *Packet
}

func (ts *proofCaptureTransport) Outbound(packet *Packet) error {
	ts.lastPacket = packet
	return nil
}

func TestIdentity(t *testing.T) {
	id := mustTestNewIdentity(t, true)

	// Test public key consistency
	pub := id.GetPublicKey()
	if len(pub) != IdentityKeySize/8 {
		t.Errorf("expected public key size %v, got %v", IdentityKeySize/8, len(pub))
	}

	// Test signing/verification
	msg := []byte("hello world")
	sig, err := id.Sign(msg)
	mustTest(t, err)
	if !id.Verify(sig, msg) {
		t.Errorf("signature verification failed")
	}

	// Test encryption/decryption
	encrypted, err := id.Encrypt(msg, nil)
	mustTest(t, err)
	decrypted, err := id.Decrypt(encrypted, nil, false)
	mustTest(t, err)
	if !bytes.Equal(msg, decrypted) {
		t.Errorf("decryption failed: expected %s, got %s", msg, decrypted)
	}
}

func TestIdentityLoading(t *testing.T) {
	id1 := mustTestNewIdentity(t, true)
	prvBytes := id1.GetPrivateKey()
	pubBytes := id1.GetPublicKey()

	// Test loading private key
	id2 := mustTestNewIdentity(t, false)
	err := id2.LoadPrivateKey(prvBytes)
	mustTest(t, err)
	if !bytes.Equal(id1.Hash, id2.Hash) {
		t.Errorf("identity hash mismatch after loading private key")
	}

	// Test loading public key
	id3 := mustTestNewIdentity(t, false)
	err = id3.LoadPublicKey(pubBytes)
	mustTest(t, err)
	if !bytes.Equal(id1.Hash, id3.Hash) {
		t.Errorf("identity hash mismatch after loading public key")
	}
}

func TestFromBytes(t *testing.T) {
	t.Parallel()

	id1 := mustTestNewIdentity(t, true)
	prvBytes := id1.GetPrivateKey()
	pubBytes := id1.GetPublicKey()

	tests := []struct {
		name    string
		input   []byte
		wantErr bool
		wantPrv bool
		wantPub []byte
	}{
		{
			name:    "valid private key bytes",
			input:   prvBytes,
			wantErr: false,
			wantPrv: true,
			wantPub: pubBytes,
		},
		{
			name:    "too short",
			input:   []byte("tooshort"),
			wantErr: true,
		},
		{
			name:    "nil input",
			input:   nil,
			wantErr: true,
		},
		{
			name:    "public key bytes are not valid private key input",
			input:   pubBytes,
			wantErr: false,
			wantPrv: true,
		},
		{
			name:    "wrong length 63 bytes",
			input:   make([]byte, 63),
			wantErr: true,
		},
		{
			name:    "wrong length 65 bytes",
			input:   make([]byte, 65),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id, err := FromBytes(tt.input, nil)
			if tt.wantErr {
				if err == nil {
					t.Errorf("FromBytes() expected error, got nil")
				}
				if id != nil {
					t.Errorf("FromBytes() expected nil identity on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("FromBytes() unexpected error: %v", err)
			}
			if id == nil {
				t.Fatal("FromBytes() returned nil identity without error")
			}
			if tt.wantPrv && id.GetPrivateKey() == nil {
				t.Errorf("FromBytes() expected identity to hold private key")
			}
			if tt.wantPub != nil && !bytes.Equal(id.GetPublicKey(), tt.wantPub) {
				t.Errorf("FromBytes() public key mismatch")
			}
		})
	}
}

func TestIdentityProveExplicit(t *testing.T) {
	t.Parallel()

	id := mustTestNewIdentity(t, true)
	ts := &proofCaptureTransport{TransportSystem: NewTransportSystem(nil)}
	ts.SetUseImplicitProof(false)

	packetHash := bytes.Repeat([]byte{0x42}, 32)
	packet := &Packet{
		PacketHash: packetHash,
		transport:  ts,
	}

	id.Prove(packet, nil)

	if ts.lastPacket == nil {
		t.Fatal("expected proof packet to be sent")
	}
	signature := id.sigPrv.Sign(packetHash)
	want := append(append([]byte{}, packetHash...), signature...)
	if !bytes.Equal(ts.lastPacket.Data, want) {
		t.Fatalf("explicit proof payload mismatch: got %x want %x", ts.lastPacket.Data, want)
	}
}

// TestValidateAnnounceShortDataWithRacket is a regression test for a panic
// where the minimum-length guard in ValidateAnnounce did not account for the
// 32-byte ratchet key present when ContextFlag is set. A packet whose Data
// was long enough to pass the base check (>=148 bytes) but too short to hold
// the ratchet+signature (>=180 bytes) sliced out of range at the signature
// read and crashed the inbound path. Such malformed packets must be rejected
// gracefully (return false) rather than panicking.
func TestValidateAnnounceShortDataWithRatchet(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	logger.SetLogLevel(LogNone) // suppress debug spam
	ts := NewTransportSystem(logger)

	const (
		keySize     = IdentityKeySize / 8 // 64
		nameHashLen = NameHashLength / 8  // 10
		sigLen      = 64
		ratchetSize = 32
	)
	baseMin := keySize + nameHashLen + 10 + sigLen                  // 148
	ratchetMin := keySize + nameHashLen + 10 + ratchetSize + sigLen // 180

	cases := []struct {
		name        string
		contextFlag int
		dataLen     int
	}{
		{"ratchet set, between base and ratchet min (crash scenario)", FlagSet, ratchetMin - 2}, // 178
		{"ratchet set, exactly ratchet min", FlagSet, ratchetMin},                               // 180
		{"ratchet set, just below base min", FlagSet, baseMin - 1},                              // 147
		{"ratchet unset, exactly base min", 0, baseMin},                                         // 148
		{"ratchet unset, one below base min", 0, baseMin - 1},                                   // 147
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			packet := &Packet{
				PacketType:  PacketAnnounce,
				ContextFlag: tc.contextFlag,
				Data:        bytes.Repeat([]byte{0xAB}, tc.dataLen),
				Raw:         bytes.Repeat([]byte{0xCD}, 200), // >=120 for debug line
			}

			// The contract: never panic, always return false for these
			// structurally-invalid/garbage payloads.
			got := ValidateAnnounce(ts, packet)
			if got {
				t.Fatalf("expected ValidateAnnounce to reject garbage payload, got true")
			}
		})
	}
}

func TestIdentityProveImplicit(t *testing.T) {
	t.Parallel()

	id := mustTestNewIdentity(t, true)
	ts := &proofCaptureTransport{TransportSystem: NewTransportSystem(nil)}
	ts.SetUseImplicitProof(true)

	packetHash := bytes.Repeat([]byte{0x24}, 32)
	packet := &Packet{
		PacketHash: packetHash,
		transport:  ts,
	}

	id.Prove(packet, nil)

	if ts.lastPacket == nil {
		t.Fatal("expected proof packet to be sent")
	}
	want := id.sigPrv.Sign(packetHash)
	if !bytes.Equal(ts.lastPacket.Data, want) {
		t.Fatalf("implicit proof payload mismatch: got %x want %x", ts.lastPacket.Data, want)
	}
}
