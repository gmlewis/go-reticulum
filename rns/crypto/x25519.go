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

// X25519PrivateKey encapsulates a standard Elliptic Curve Diffie-Hellman (ECDH) private key on the Curve25519 architecture.
// It is designed to safely handle cryptographic key exchanges, ensuring robust and performant asymmetric encryption within the network layer.
type X25519PrivateKey struct {
	priv *ecdh.PrivateKey
}

// GenerateX25519PrivateKey securely provisions a new Curve25519 private key leveraging the system's cryptographically secure random number generator.
// It returns a robustly initialized X25519PrivateKey or an error if entropy cannot be safely gathered.
func GenerateX25519PrivateKey() (*X25519PrivateKey, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &X25519PrivateKey{priv: priv}, nil
}

// NewX25519PrivateKeyFromBytes deterministically reconstitutes an X25519PrivateKey from a raw 32-byte scalar seed.
// This is essential for resuming previous identities or deserializing keys received from trusted persistent storage.
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

// PrivateBytes exposes the raw 32-byte scalar representation of the underlying Curve25519 private key.
// Callers must exercise extreme caution to prevent leakage when retaining or transmitting this sensitive value.
func (k *X25519PrivateKey) PrivateBytes() []byte {
	return k.priv.Bytes()
}

// PublicKey calculates and returns the corresponding X25519PublicKey derived deterministically from this private key.
// It securely extracts the associated public material required for establishing asymmetric communication channels.
func (k *X25519PrivateKey) PublicKey() *X25519PublicKey {
	pub := k.priv.PublicKey()
	return &X25519PublicKey{pub: pub}
}

// Exchange performs a secure Elliptic Curve Diffie-Hellman key agreement with the provided peer public key.
// It derives a shared cryptographic secret that can be safely used to establish a symmetric encryption channel.
func (k *X25519PrivateKey) Exchange(peerPublicKey *X25519PublicKey) ([]byte, error) {
	return k.priv.ECDH(peerPublicKey.pub)
}

// X25519PublicKey encapsulates the standard Curve25519 public key material used in key agreement operations.
// It serves as a shareable identity component for establishing secure peer-to-peer connections.
type X25519PublicKey struct {
	pub *ecdh.PublicKey
}

// NewX25519PublicKeyFromBytes validates and constructs an X25519PublicKey from a raw 32-byte slice.
// It strictly enforces the byte length to ensure the integrity of the key before it is used in ECDH calculations.
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

// PublicBytes yields the 32-byte encoded slice representing the public key.
// This data can be freely broadcasted or serialized to facilitate key exchanges across the network.
func (k *X25519PublicKey) PublicBytes() []byte {
	return k.pub.Bytes()
}
