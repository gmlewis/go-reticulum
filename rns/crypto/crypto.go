// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package crypto provides the cryptographic primitives used by the Reticulum
// Network Stack.
//
// It includes:
//   - Hashing: SHA-256 and SHA-512.
//   - HMAC: keyed-hash message authentication codes over SHA-256.
//   - HKDF: HMAC-based key derivation.
//   - AES: CBC mode with 128- and 256-bit keys.
//   - Ed25519: digital signatures and verification.
//   - X25519: ECDH key agreement.
//   - Tokens: a Fernet-like authenticated encryption envelope.
//   - PKCS7: block padding and unpadding.
package crypto
