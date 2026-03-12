// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package crypto provides the cryptographic primitives required by the
// Reticulum Network Stack.
//
// This package implements or wraps the following primitives:
//   - Hashing: SHA-256 and SHA-512.
//   - HMAC: Keyed-hash message authentication codes.
//   - HKDF: HMAC-based Extract-and-Expand Key Derivation Function.
//   - AES: Advanced Encryption Standard in CBC mode (128 and 256 bits).
//   - Ed25519: Digital signatures and verification.
//   - X25519: Elliptic Curve Diffie-Hellman key exchange.
//   - Tokens: A modified Fernet-like token format for encrypted packets.
//   - PKCS7: Data padding and unpadding.
package crypto
