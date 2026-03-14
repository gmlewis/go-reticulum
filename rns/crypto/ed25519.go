// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/ed25519"
	"errors"
)

// Ed25519PrivateKey firmly encapsulates a standard Ed25519 private key.
// It empowers node identities to securely sign payloads and assert non-repudiable presence within the Reticulum network topology.
type Ed25519PrivateKey struct {
	priv ed25519.PrivateKey
}

// GenerateEd25519PrivateKey securely provisions a fresh, cryptographically strong Ed25519 private key.
// It delegates to the system's underlying secure random number generator to ensure high entropy and collision resistance.
func GenerateEd25519PrivateKey() (*Ed25519PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	return &Ed25519PrivateKey{priv: priv}, nil
}

// NewEd25519PrivateKeyFromBytes reconstructs a valid Ed25519PrivateKey specifically from its raw 32-byte seed.
// This allows deterministic resumption of a cryptographic identity from persistent, trusted storage mediums.
func NewEd25519PrivateKeyFromBytes(data []byte) (*Ed25519PrivateKey, error) {
	if len(data) != 32 {
		return nil, errors.New("invalid private key length")
	}
	priv := ed25519.NewKeyFromSeed(data)
	return &Ed25519PrivateKey{priv: priv}, nil
}

// PrivateBytes extracts the raw 32-byte scalar seed underlying the Ed25519 private key.
// Callers must guarantee strict confidentiality of this slice, as its disclosure would compromise the identity entirely.
func (k *Ed25519PrivateKey) PrivateBytes() []byte {
	return k.priv.Seed()
}

// PublicKey deterministically computes and returns the Ed25519PublicKey mathematically bound to this private key.
// It safely extracts the verifiable component required for remote peers to validate signatures originating from this identity.
func (k *Ed25519PrivateKey) PublicKey() *Ed25519PublicKey {
	pub := k.priv.Public().(ed25519.PublicKey)
	return &Ed25519PublicKey{pub: pub}
}

// Sign processes an arbitrary message payload and returns a deterministic cryptographic signature bound to this private key.
// It leverages the Ed25519 algorithm to guarantee authenticity and non-repudiation of the supplied message.
func (k *Ed25519PrivateKey) Sign(message []byte) []byte {
	return ed25519.Sign(k.priv, message)
}

// Ed25519PublicKey encapsulates the shareable public material of an Ed25519 key pair.
// It serves as a public identity mechanism that other network participants can utilize to rigorously verify signed announcements and messages.
type Ed25519PublicKey struct {
	pub ed25519.PublicKey
}

// NewEd25519PublicKeyFromBytes securely imports and structures an Ed25519PublicKey from a raw 32-byte slice.
// It enforces rigid bounds checking to guarantee the integrity of the public key material before deployment in signature validation routines.
func NewEd25519PublicKeyFromBytes(data []byte) (*Ed25519PublicKey, error) {
	if len(data) != 32 {
		return nil, errors.New("invalid public key length")
	}
	return &Ed25519PublicKey{pub: ed25519.PublicKey(data)}, nil
}

// PublicBytes yields the standard 32-byte slice representation of the public key.
// This slice is suitable for broadcasting or inclusion within routing tables to propagate identity awareness.
func (k *Ed25519PublicKey) PublicBytes() []byte {
	return []byte(k.pub)
}

// Verify interrogates a given cryptographic signature against the provided message payload using this public key.
// It returns a boolean indicating absolute validity, strictly rejecting any tampered or mathematically inconsistent signatures.
func (k *Ed25519PublicKey) Verify(signature, message []byte) bool {
	return ed25519.Verify(k.pub, message, signature)
}
