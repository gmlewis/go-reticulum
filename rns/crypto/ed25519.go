// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/ed25519"
	"errors"
)

// Ed25519PrivateKey is an Ed25519 private key. Reticulum identities use it to
// sign announces, path requests, proofs, and LXMF payloads.
type Ed25519PrivateKey struct {
	priv ed25519.PrivateKey
}

// GenerateEd25519PrivateKey returns a new Ed25519 private key seeded from the
// process-wide randomness source (see SetEntropySource). It returns an error
// rather than a weak key when that source cannot supply entropy.
func GenerateEd25519PrivateKey() (*Ed25519PrivateKey, error) {
	seed := make([]byte, ed25519.SeedSize)
	if _, err := RandomBytes(seed); err != nil {
		return nil, err
	}
	return NewEd25519PrivateKeyFromBytes(seed)
}

// NewEd25519PrivateKeyFromBytes builds an Ed25519PrivateKey from its raw
// 32-byte seed, as stored on disk or received in an announce.
func NewEd25519PrivateKeyFromBytes(data []byte) (*Ed25519PrivateKey, error) {
	if len(data) != 32 {
		return nil, errors.New("invalid private key length")
	}
	priv := ed25519.NewKeyFromSeed(data)
	return &Ed25519PrivateKey{priv: priv}, nil
}

// PrivateBytes returns the raw 32-byte seed of the key. The caller must keep
// the returned slice confidential.
func (k *Ed25519PrivateKey) PrivateBytes() []byte {
	return k.priv.Seed()
}

// PublicKey returns the Ed25519PublicKey belonging to k.
func (k *Ed25519PrivateKey) PublicKey() *Ed25519PublicKey {
	pub := k.priv.Public().(ed25519.PublicKey)
	return &Ed25519PublicKey{pub: pub}
}

// Sign returns the Ed25519 signature of message.
func (k *Ed25519PrivateKey) Sign(message []byte) []byte {
	return ed25519.Sign(k.priv, message)
}

// Ed25519PublicKey is the shareable half of an Ed25519 key pair, used to verify
// signatures made by the matching private key.
type Ed25519PublicKey struct {
	pub ed25519.PublicKey
}

// NewEd25519PublicKeyFromBytes builds an Ed25519PublicKey from a raw 32-byte
// slice and rejects any other length.
func NewEd25519PublicKeyFromBytes(data []byte) (*Ed25519PublicKey, error) {
	if len(data) != 32 {
		return nil, errors.New("invalid public key length")
	}
	return &Ed25519PublicKey{pub: ed25519.PublicKey(data)}, nil
}

// PublicBytes returns the 32-byte encoding of the public key, as sent in
// announces and destination hashes.
func (k *Ed25519PublicKey) PublicBytes() []byte {
	return []byte(k.pub)
}

// Verify reports whether signature is a valid Ed25519 signature of message
// under this public key.
func (k *Ed25519PublicKey) Verify(signature, message []byte) bool {
	return ed25519.Verify(k.pub, message, signature)
}
