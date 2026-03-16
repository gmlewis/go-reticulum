// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"testing"
)

func TestEd25519(t *testing.T) {
	t.Parallel()
	priv, err := GenerateEd25519PrivateKey()
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	pub := priv.PublicKey()
	if pub == nil {
		t.Fatal("failed to get public key")
	}

	privBytes := priv.PrivateBytes()
	if len(privBytes) != 32 {
		t.Errorf("expected 32 bytes for private key, got %v", len(privBytes))
	}

	pubBytes := pub.PublicBytes()
	if len(pubBytes) != 32 {
		t.Errorf("expected 32 bytes for public key, got %v", len(pubBytes))
	}

	priv2, err := NewEd25519PrivateKeyFromBytes(privBytes)
	if err != nil {
		t.Fatalf("failed to create private key from bytes: %v", err)
	}

	if !bytes.Equal(priv.PrivateBytes(), priv2.PrivateBytes()) {
		t.Error("private keys do not match")
	}

	pub2, err := NewEd25519PublicKeyFromBytes(pubBytes)
	if err != nil {
		t.Fatalf("failed to create public key from bytes: %v", err)
	}

	if !bytes.Equal(pub.PublicBytes(), pub2.PublicBytes()) {
		t.Error("public keys do not match")
	}

	message := []byte("hello reticulum")
	signature := priv.Sign(message)
	if len(signature) != 64 {
		t.Errorf("expected 64 bytes for signature, got %v", len(signature))
	}

	if !pub.Verify(signature, message) {
		t.Error("failed to verify signature")
	}

	if pub.Verify(signature, []byte("wrong message")) {
		t.Error("signature verified against wrong message")
	}

	badSignature := make([]byte, 64)
	copy(badSignature, signature)
	badSignature[0] ^= 0xFF
	if pub.Verify(badSignature, message) {
		t.Error("signature verified with bad signature")
	}
}

func TestNewEd25519PrivateKeyFromBytes_Errors(t *testing.T) {
	t.Parallel()
	if _, err := NewEd25519PrivateKeyFromBytes(make([]byte, 31)); err == nil {
		t.Error("expected error for short private key")
	}

	if _, err := NewEd25519PrivateKeyFromBytes(make([]byte, 33)); err == nil {
		t.Error("expected error for long private key")
	}
}

func TestNewEd25519PublicKeyFromBytes_Errors(t *testing.T) {
	t.Parallel()
	if _, err := NewEd25519PublicKeyFromBytes(make([]byte, 31)); err == nil {
		t.Error("expected error for short public key")
	}

	if _, err := NewEd25519PublicKeyFromBytes(make([]byte, 33)); err == nil {
		t.Error("expected error for long public key")
	}
}
