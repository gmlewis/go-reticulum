// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"testing"
)

func TestX25519(t *testing.T) {
	t.Parallel()
	priv1, err := GenerateX25519PrivateKey()
	if err != nil {
		t.Fatalf("failed to generate private key 1: %v", err)
	}

	priv2, err := GenerateX25519PrivateKey()
	if err != nil {
		t.Fatalf("failed to generate private key 2: %v", err)
	}

	pub1 := priv1.PublicKey()
	pub2 := priv2.PublicKey()

	ss1, err := priv1.Exchange(pub2)
	if err != nil {
		t.Fatalf("exchange 1-2 failed: %v", err)
	}

	ss2, err := priv2.Exchange(pub1)
	if err != nil {
		t.Fatalf("exchange 2-1 failed: %v", err)
	}

	if !bytes.Equal(ss1, ss2) {
		t.Error("shared secrets do not match")
	}

	privBytes := priv1.PrivateBytes()
	if len(privBytes) != 32 {
		t.Errorf("expected 32 bytes for private key, got %v", len(privBytes))
	}

	pubBytes := pub1.PublicBytes()
	if len(pubBytes) != 32 {
		t.Errorf("expected 32 bytes for public key, got %v", len(pubBytes))
	}

	priv3, err := NewX25519PrivateKeyFromBytes(privBytes)
	if err != nil {
		t.Fatalf("failed to create private key from bytes: %v", err)
	}

	if !bytes.Equal(priv1.PrivateBytes(), priv3.PrivateBytes()) {
		t.Error("private keys do not match")
	}

	pub3, err := NewX25519PublicKeyFromBytes(pubBytes)
	if err != nil {
		t.Fatalf("failed to create public key from bytes: %v", err)
	}

	if !bytes.Equal(pub1.PublicBytes(), pub3.PublicBytes()) {
		t.Error("public keys do not match")
	}
}

func TestNewX25519PrivateKeyFromBytes_Errors(t *testing.T) {
	t.Parallel()
	if _, err := NewX25519PrivateKeyFromBytes(make([]byte, 31)); err == nil {
		t.Error("expected error for short private key")
	}

	if _, err := NewX25519PrivateKeyFromBytes(make([]byte, 33)); err == nil {
		t.Error("expected error for long private key")
	}
}

func TestNewX25519PublicKeyFromBytes_Errors(t *testing.T) {
	t.Parallel()
	if _, err := NewX25519PublicKeyFromBytes(make([]byte, 31)); err == nil {
		t.Error("expected error for short public key")
	}

	if _, err := NewX25519PublicKeyFromBytes(make([]byte, 33)); err == nil {
		t.Error("expected error for long public key")
	}
}
