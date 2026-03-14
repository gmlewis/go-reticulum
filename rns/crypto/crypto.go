// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

// Package crypto delivers the foundational suite of cryptographic primitives strictly required by the Reticulum Network Stack.
//
// This package meticulously implements or securely wraps essential primitives to guarantee system-wide confidentiality and integrity:
//   - Hashing: SHA-256 and SHA-512 for robust collision-resistant digests.
//   - HMAC: Keyed-hash message authentication codes safeguarding payload authenticity.
//   - HKDF: HMAC-based Extract-and-Expand Key Derivation Function for secure key material expansion.
//   - AES: Advanced Encryption Standard operating in CBC mode (128 and 256 bits) for symmetric data encapsulation.
//   - Ed25519: High-performance digital signatures and deterministic verification.
//   - X25519: Secure Elliptic Curve Diffie-Hellman (ECDH) for peer-to-peer key exchange.
//   - Tokens: A custom, highly resilient Fernet-like envelope format for securely orchestrating encrypted packet distribution.
//   - PKCS7: Deterministic cryptographic data padding and unpadding conforming to standard block sizes.
package crypto
