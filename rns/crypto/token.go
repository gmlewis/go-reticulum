// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	// TokenOverhead defines the additional byte length introduced when cryptographically sealing a payload within a Token.
	// It accounts for both the initialization vector and the appended HMAC signature used to ensure structural integrity and authenticity.
	TokenOverhead = 48 // Bytes
)

// Token implements a highly secure, authenticated symmetric encryption envelope reminiscent of the Fernet specification.
// It orchestrates both signing and encryption keys, using either AES-128 or AES-256 in CBC mode paired with an SHA-256 HMAC to guarantee data confidentiality and non-repudiation.
type Token struct {
	signingKey    []byte
	encryptionKey []byte
	isAES256      bool
}

// GenerateTokenKey securely derives a new cryptographically random key tailored for Token operations.
// It provisions a 256-bit key for AES-128 operations or a 512-bit key for AES-256 operations, sourcing from the secure random generator.
func GenerateTokenKey(aes256 bool) ([]byte, error) {
	size := 32
	if aes256 {
		size = 64
	}
	key := make([]byte, size)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// NewToken validates the provided symmetric key and initializes a Token instance ready for cryptographic operations.
// It splits the provided key material into distinct signing and encryption components, inferring the correct AES block mode based on the key's length.
func NewToken(key []byte) (*Token, error) {
	if len(key) == 32 {
		return &Token{
			signingKey:    key[:16],
			encryptionKey: key[16:],
			isAES256:      false,
		}, nil
	} else if len(key) == 64 {
		return &Token{
			signingKey:    key[:32],
			encryptionKey: key[32:],
			isAES256:      true,
		}, nil
	}
	return nil, fmt.Errorf("token key must be 256 or 512 bits, not %v", len(key)*8)
}

// VerifyHMAC validates the integrity and authenticity of the appended cryptographic signature on the provided token bytes.
// It isolates the HMAC and recalculates it across the preceding payload, ensuring no tampering has occurred in transit before decryption is attempted.
func (t *Token) VerifyHMAC(token []byte) bool {
	if len(token) <= 32 {
		return false
	}
	receivedHMAC := token[len(token)-32:]
	signedParts := token[:len(token)-32]

	h := hmac.New(sha256.New, t.signingKey)
	h.Write(signedParts)
	expectedHMAC := h.Sum(nil)

	return hmac.Equal(receivedHMAC, expectedHMAC)
}

// Encrypt encapsulates the raw plaintext data into a secure Token format using the configured encryption and signing keys.
// It introduces a fresh initialization vector, applies strict PKCS#7 padding, encrypts the payload via AES-CBC, and securely binds the result with an HMAC signature.
func (t *Token) Encrypt(data []byte) ([]byte, error) {
	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}

	padded := PKCS7Pad(data, 16)
	var ciphertext []byte
	var err error

	if t.isAES256 {
		ciphertext, err = AES256CBCEncrypt(padded, t.encryptionKey, iv)
	} else {
		ciphertext, err = AES128CBCEncrypt(padded, t.encryptionKey, iv)
	}

	if err != nil {
		return nil, err
	}

	signedParts := append(iv, ciphertext...)
	h := hmac.New(sha256.New, t.signingKey)
	h.Write(signedParts)
	mac := h.Sum(nil)

	return append(signedParts, mac...), nil
}

// Decrypt securely unwraps and validates a Token, extracting the original plaintext payload.
// It strictly enforces HMAC verification prior to executing AES-CBC decryption and removes the PKCS#7 padding, returning an error if any structural or cryptographic discrepancies are detected.
func (t *Token) Decrypt(token []byte) ([]byte, error) {
	if !t.VerifyHMAC(token) {
		return nil, errors.New("token HMAC was invalid")
	}

	if len(token) < 16+32 {
		return nil, errors.New("token too short")
	}

	iv := token[:16]
	ciphertext := token[16 : len(token)-32]

	var padded []byte
	var err error

	if t.isAES256 {
		padded, err = AES256CBCDecrypt(ciphertext, t.encryptionKey, iv)
	} else {
		padded, err = AES128CBCDecrypt(ciphertext, t.encryptionKey, iv)
	}

	if err != nil {
		return nil, fmt.Errorf("could not decrypt token: %v", err)
	}

	return PKCS7Unpad(padded)
}
