// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"testing"
)

func TestToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		isAES256 bool
	}{
		{"AES128", false},
		{"AES256", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			key, err := GenerateTokenKey(tt.isAES256)
			if err != nil {
				t.Fatalf("failed to generate token key: %v", err)
			}

			token := mustTestNewToken(t, key)
			if err != nil {
				t.Fatalf("failed to create token: %v", err)
			}

			data := []byte("hello reticulum token")
			encrypted, err := token.Encrypt(data)
			if err != nil {
				t.Fatalf("failed to encrypt data: %v", err)
			}

			if !token.VerifyHMAC(encrypted) {
				t.Error("HMAC verification failed")
			}

			decrypted, err := token.Decrypt(encrypted)
			if err != nil {
				t.Fatalf("failed to decrypt data: %v", err)
			}

			if !bytes.Equal(data, decrypted) {
				t.Errorf("expected %v, got %v", data, decrypted)
			}
		})
	}
}

func TestNewToken_Errors(t *testing.T) {
	t.Parallel()
	if _, err := NewToken(make([]byte, 31)); err == nil {
		t.Error("expected error for 31-byte key")
	}

	if _, err := NewToken(make([]byte, 33)); err == nil {
		t.Error("expected error for 33-byte key")
	}

	if _, err := NewToken(make([]byte, 63)); err == nil {
		t.Error("expected error for 63-byte key")
	}

	if _, err := NewToken(make([]byte, 65)); err == nil {
		t.Error("expected error for 65-byte key")
	}
}

func TestToken_VerifyHMAC_Invalid(t *testing.T) {
	t.Parallel()
	key, _ := GenerateTokenKey(false)
	token := mustTestNewToken(t, key)

	if token.VerifyHMAC(make([]byte, 32)) {
		t.Error("expected false for short token")
	}

	data := []byte("test")
	encrypted, _ := token.Encrypt(data)

	// Corrupt HMAC
	encrypted[len(encrypted)-1] ^= 0xFF
	if token.VerifyHMAC(encrypted) {
		t.Error("expected false for corrupted HMAC")
	}

	// Corrupt data
	encrypted, _ = token.Encrypt(data)
	encrypted[0] ^= 0xFF
	if token.VerifyHMAC(encrypted) {
		t.Error("expected false for corrupted data")
	}
}

func TestToken_Decrypt_Invalid(t *testing.T) {
	t.Parallel()
	key, _ := GenerateTokenKey(false)
	token := mustTestNewToken(t, key)

	data := []byte("test")
	encrypted, _ := token.Encrypt(data)

	// Corrupt HMAC
	badHMAC := make([]byte, len(encrypted))
	copy(badHMAC, encrypted)
	badHMAC[len(badHMAC)-1] ^= 0xFF
	_, err := token.Decrypt(badHMAC)
	if err == nil {
		t.Error("expected error for invalid HMAC")
	}

	// Token too short
	_, err = token.Decrypt(make([]byte, 47)) // 16 (IV) + 32 (HMAC) - 1
	if err == nil {
		t.Error("expected error for short token")
	}
}
