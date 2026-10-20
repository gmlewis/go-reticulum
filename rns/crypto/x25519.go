// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/ecdh"
	"errors"
)

// X25519PrivateKey is a Curve25519 private key for ECDH key agreement, used
// to establish ephemeral link keys.
type X25519PrivateKey struct {
	priv *ecdh.PrivateKey
}

// GenerateX25519PrivateKey returns a new Curve25519 private key drawn from the
// process-wide randomness source (see SetEntropySource).
func GenerateX25519PrivateKey() (*X25519PrivateKey, error) {
	seed := make([]byte, 32)
	if _, err := RandomBytes(seed); err != nil {
		return nil, err
	}
	return NewX25519PrivateKeyFromBytes(seed)
}

// NewX25519PrivateKeyFromBytes builds an X25519PrivateKey from its raw 32-byte
// scalar, as stored on disk or received from a peer.
func NewX25519PrivateKeyFromBytes(data []byte) (*X25519PrivateKey, error) {
	if len(data) != 32 {
		return nil, errors.New("invalid private key length")
	}
	priv, err := ecdh.X25519().NewPrivateKey(data)
	if err != nil {
		return nil, err
	}
	return &X25519PrivateKey{priv: priv}, nil
}

// PrivateBytes returns the raw 32-byte scalar of the key. The caller must keep
// the returned slice confidential.
func (k *X25519PrivateKey) PrivateBytes() []byte {
	return k.priv.Bytes()
}

// PublicKey returns the X25519PublicKey derived from k.
func (k *X25519PrivateKey) PublicKey() *X25519PublicKey {
	pub := k.priv.PublicKey()
	return &X25519PublicKey{pub: pub}
}

// Exchange performs ECDH with the peer's public key and returns the shared
// secret.
func (k *X25519PrivateKey) Exchange(peerPublicKey *X25519PublicKey) ([]byte, error) {
	return k.priv.ECDH(peerPublicKey.pub)
}

// X25519PublicKey is the shareable half of an X25519 key pair.
type X25519PublicKey struct {
	pub *ecdh.PublicKey
}

// NewX25519PublicKeyFromBytes builds an X25519PublicKey from a raw 32-byte
// slice and rejects any other length.
func NewX25519PublicKeyFromBytes(data []byte) (*X25519PublicKey, error) {
	if len(data) != 32 {
		return nil, errors.New("invalid public key length")
	}
	pub, err := ecdh.X25519().NewPublicKey(data)
	if err != nil {
		return nil, err
	}
	return &X25519PublicKey{pub: pub}, nil
}

// PublicBytes returns the 32-byte encoding of the public key.
func (k *X25519PublicKey) PublicBytes() []byte {
	return k.pub.Bytes()
}
