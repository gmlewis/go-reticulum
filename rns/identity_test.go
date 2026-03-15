// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"testing"
)

func TestIdentity(t *testing.T) {
	id, err := NewIdentity(true)
	if err != nil {
		t.Fatal(err)
	}

	// Test public key consistency
	pub := id.GetPublicKey()
	if len(pub) != IdentityKeySize/8 {
		t.Errorf("expected public key size %v, got %v", IdentityKeySize/8, len(pub))
	}

	// Test signing/verification
	msg := []byte("hello world")
	sig, err := id.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !id.Verify(sig, msg) {
		t.Errorf("signature verification failed")
	}

	// Test encryption/decryption
	encrypted, err := id.Encrypt(msg, nil)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := id.Decrypt(encrypted, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(msg, decrypted) {
		t.Errorf("decryption failed: expected %s, got %s", msg, decrypted)
	}
}

func TestIdentityLoading(t *testing.T) {
	id1, _ := NewIdentity(true)
	prvBytes := id1.GetPrivateKey()
	pubBytes := id1.GetPublicKey()

	// Test loading private key
	id2, _ := NewIdentity(false)
	err := id2.LoadPrivateKey(prvBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(id1.Hash, id2.Hash) {
		t.Errorf("identity hash mismatch after loading private key")
	}

	// Test loading public key
	id3, _ := NewIdentity(false)
	err = id3.LoadPublicKey(pubBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(id1.Hash, id3.Hash) {
		t.Errorf("identity hash mismatch after loading public key")
	}
}

func TestFromBytes(t *testing.T) {
	t.Parallel()

	id1, err := NewIdentity(true)
	if err != nil {
		t.Fatal(err)
	}
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
			id, err := FromBytes(tt.input)
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
