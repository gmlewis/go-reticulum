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

// AES128CBCEncrypt encrypts plaintext with AES-128 in CBC mode. The key must
// be exactly 16 bytes and iv 16 bytes; plaintext must be a whole number of
// 16-byte blocks.
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

// AES128CBCDecrypt decrypts AES-128 CBC ciphertext with the same key and iv
// used to encrypt it. Both must be exactly 16 bytes.
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

// AES256CBCEncrypt encrypts plaintext with AES-256 in CBC mode. The key must
// be exactly 32 bytes and iv 16 bytes; plaintext must be a whole number of
// 16-byte blocks.
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

// AES256CBCDecrypt decrypts AES-256 CBC ciphertext with the same key and iv
// used to encrypt it. Both must be exactly 32 and 16 bytes respectively.
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
