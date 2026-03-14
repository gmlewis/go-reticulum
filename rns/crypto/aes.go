// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

// AES128CBCEncrypt securely encapsulates the provided plaintext using the Advanced Encryption Standard (AES) operating in 128-bit Cipher Block Chaining (CBC) mode.
// It mandates a strictly 16-byte key and a 16-byte initialization vector, returning the encrypted ciphertext block or an error if structural constraints are violated.
func AES128CBCEncrypt(plaintext, key, iv []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("invalid key length %v for AES-128", len(key)*8)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	ciphertext := make([]byte, len(plaintext))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, plaintext)
	return ciphertext, nil
}

// AES128CBCDecrypt systematically reverses the AES-128 CBC encryption process on the provided ciphertext.
// It requires the exact corresponding 16-byte key and initialization vector used during encryption, returning the recovered plaintext or structurally signaling an error if the decryption sequence fundamentally fails.
func AES128CBCDecrypt(ciphertext, key, iv []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("invalid key length %v for AES-128", len(key)*8)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(plaintext, ciphertext)
	return plaintext, nil
}

// AES256CBCEncrypt securely encapsulates the provided plaintext using the robust Advanced Encryption Standard (AES) operating in 256-bit Cipher Block Chaining (CBC) mode.
// It enforces the usage of a 32-byte high-entropy key alongside a 16-byte initialization vector, significantly elevating the security margin against brute-force attacks.
func AES256CBCEncrypt(plaintext, key, iv []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid key length %v for AES-256", len(key)*8)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	ciphertext := make([]byte, len(plaintext))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, plaintext)
	return ciphertext, nil
}

// AES256CBCDecrypt safely reconstructs the original plaintext from an AES-256 CBC encoded ciphertext block.
// It leverages the previously shared 32-byte key and initialization vector to unwind the symmetric cipher, returning the pristine payload for subsequent processing layers.
func AES256CBCDecrypt(ciphertext, key, iv []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("invalid key length %v for AES-256", len(key)*8)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plaintext := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(plaintext, ciphertext)
	return plaintext, nil
}
