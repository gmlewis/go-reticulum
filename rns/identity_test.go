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
