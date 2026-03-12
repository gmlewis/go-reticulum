// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/ed25519"
	"errors"
)

// Ed25519PrivateKey represents an Ed25519 private key.
type Ed25519PrivateKey struct {
	priv ed25519.PrivateKey
}

// GenerateEd25519PrivateKey generates a new Ed25519 private key.
func GenerateEd25519PrivateKey() (*Ed25519PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	return &Ed25519PrivateKey{priv: priv}, nil
}

// NewEd25519PrivateKeyFromBytes creates an Ed25519 private key from its seed bytes (32 bytes).
func NewEd25519PrivateKeyFromBytes(data []byte) (*Ed25519PrivateKey, error) {
	if len(data) != 32 {
		return nil, errors.New("invalid private key length")
	}
	priv := ed25519.NewKeyFromSeed(data)
	return &Ed25519PrivateKey{priv: priv}, nil
}

// PrivateBytes returns the seed bytes of the private key.
func (k *Ed25519PrivateKey) PrivateBytes() []byte {
	return k.priv.Seed()
}

// PublicKey returns the corresponding public key.
func (k *Ed25519PrivateKey) PublicKey() *Ed25519PublicKey {
	pub := k.priv.Public().(ed25519.PublicKey)
	return &Ed25519PublicKey{pub: pub}
}

// Sign signs a message using the private key.
func (k *Ed25519PrivateKey) Sign(message []byte) []byte {
	return ed25519.Sign(k.priv, message)
}

// Ed25519PublicKey represents an Ed25519 public key.
type Ed25519PublicKey struct {
	pub ed25519.PublicKey
}

// NewEd25519PublicKeyFromBytes creates an Ed25519 public key from bytes.
func NewEd25519PublicKeyFromBytes(data []byte) (*Ed25519PublicKey, error) {
	if len(data) != 32 {
		return nil, errors.New("invalid public key length")
	}
	return &Ed25519PublicKey{pub: ed25519.PublicKey(data)}, nil
}

// PublicBytes returns the bytes of the public key.
func (k *Ed25519PublicKey) PublicBytes() []byte {
	return []byte(k.pub)
}

// Verify verifies a signature against a message.
func (k *Ed25519PublicKey) Verify(signature, message []byte) bool {
	return ed25519.Verify(k.pub, message, signature)
}
