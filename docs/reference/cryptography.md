# Cryptographic Primitives & Specifications

Reticulum is a cryptography-first network stack. All packets traversing the network are signed, verified, authenticated, and encrypted using established, non-negotiable cryptographic primitives.

---

## Core Primitives in Pure Go

Go Reticulum implements all cryptographic primitives using the Go standard library (`crypto/*`):

| Function | Algorithm | Go Implementation | Notes |
|----------|-----------|-------------------|-------|
| **Digital Signatures** | Ed25519 (RFC 8032) | `crypto/ed25519` | Used for identity verification and packet signatures. |
| **Key Exchange (ECDH)** | X25519 (RFC 7748) | `crypto/ecdh` | Ephemeral Diffie-Hellman key exchange for Links. |
| **Symmetric Cipher** | AES-128-CBC | `crypto/aes`, `crypto/cipher` | Packet payload encryption. |
| **Message Authentication** | HMAC-SHA256 | `crypto/hmac`, `crypto/sha256` | Payload authentication and integrity. |
| **Key Derivation** | HKDF-SHA256 (RFC 5869) | Go Standard Library | Derives encryption and authentication keys from shared secrets. |
| **Address Hashing** | Truncated SHA-256 (128-bit / 16-byte) | `crypto/sha256` | Truncated hash of public key forms 16-byte destination address. |
| **Entropy Source** | CSPRNG | `crypto/rand` | System cryptographic random source. |

---

## Reticulum Destination Addresses

Every Reticulum destination address is a 16-byte (128-bit) cryptographic hash derived from:

$$\text{Address} = \text{SHA256}(\text{Public Key} \parallel \text{App Name} \parallel \text{Aspects})[0:16]$$

Because addresses are mathematical derivations of cryptographic keys, addresses cannot be spoofed without possession of the private key.

---

## Link Security & Forward Secrecy

When establishing a bidirectional **Link**:
1. Both initiator and responder generate ephemeral X25519 keypairs.
2. An ECDH exchange computes a unique shared secret.
3. HKDF derives separate symmetric encryption and HMAC keys.
4. If a link key is compromised in the future, past communications cannot be decrypted (**Perfect Forward Secrecy**).
