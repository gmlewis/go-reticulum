// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"testing"
)

func TestAES(t *testing.T) {
	t.Parallel()
	key128 := make([]byte, 16)
	key256 := make([]byte, 32)
	iv := make([]byte, 16)
	data := []byte("hello world12345") // 16 bytes

	// AES-128
	encrypted, err := AES128CBCEncrypt(data, key128, iv)
	mustTest(t, err)
	decrypted, err := AES128CBCDecrypt(encrypted, key128, iv)
	mustTest(t, err)
	if !bytes.Equal(data, decrypted) {
		t.Errorf("AES-128 decrypted data mismatch")
	}

	// AES-256
	encrypted, err = AES256CBCEncrypt(data, key256, iv)
	mustTest(t, err)
	decrypted, err = AES256CBCDecrypt(encrypted, key256, iv)
	mustTest(t, err)
	if !bytes.Equal(data, decrypted) {
		t.Errorf("AES-256 decrypted data mismatch")
	}

	// Invalid key lengths
	badKey := make([]byte, 15)
	if _, err := AES128CBCEncrypt(data, badKey, iv); err == nil {
		t.Error("expected error for bad AES-128 key length")
	}
	if _, err := AES128CBCDecrypt(data, badKey, iv); err == nil {
		t.Error("expected error for bad AES-128 key length")
	}

	badKey = make([]byte, 31)
	if _, err := AES256CBCEncrypt(data, badKey, iv); err == nil {
		t.Error("expected error for bad AES-256 key length")
	}
	if _, err := AES256CBCDecrypt(data, badKey, iv); err == nil {
		t.Error("expected error for bad AES-256 key length")
	}
}
