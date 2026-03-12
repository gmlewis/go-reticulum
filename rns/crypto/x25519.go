// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
)

// X25519PrivateKey represents an X25519 private key.
type X25519PrivateKey struct {
	priv *ecdh.PrivateKey
}

// GenerateX25519PrivateKey generates a new X25519 private key.
func GenerateX25519PrivateKey() (*X25519PrivateKey, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &X25519PrivateKey{priv: priv}, nil
}

// NewX25519PrivateKeyFromBytes creates an X25519 private key from bytes (32 bytes).
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

// PrivateBytes returns the bytes of the private key.
func (k *X25519PrivateKey) PrivateBytes() []byte {
	return k.priv.Bytes()
}

// PublicKey returns the corresponding public key.
func (k *X25519PrivateKey) PublicKey() *X25519PublicKey {
	pub := k.priv.PublicKey()
	return &X25519PublicKey{pub: pub}
}

// Exchange performs a key exchange with the given public key.
func (k *X25519PrivateKey) Exchange(peerPublicKey *X25519PublicKey) ([]byte, error) {
	return k.priv.ECDH(peerPublicKey.pub)
}

// X25519PublicKey represents an X25519 public key.
type X25519PublicKey struct {
	pub *ecdh.PublicKey
}

// NewX25519PublicKeyFromBytes creates an X25519 public key from bytes.
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

// PublicBytes returns the bytes of the public key.
func (k *X25519PublicKey) PublicBytes() []byte {
	return k.pub.Bytes()
}
