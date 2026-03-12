// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"testing"
)

func TestPKCS7(t *testing.T) {
	tests := []struct {
		data      []byte
		blockSize int
	}{
		{[]byte("hello world"), 16},
		{[]byte("1234567890123456"), 16},
		{[]byte(""), 16},
		{[]byte("a"), 8},
		{[]byte("12345678"), 8},
	}

	for _, tt := range tests {
		padded := PKCS7Pad(tt.data, tt.blockSize)
		if len(padded)%tt.blockSize != 0 {
			t.Errorf("PKCS7Pad length (%v) not multiple of %v", len(padded), tt.blockSize)
		}

		unpadded, err := PKCS7Unpad(padded)
		if err != nil {
			t.Fatalf("PKCS7Unpad failed: %v", err)
		}

		if !bytes.Equal(unpadded, tt.data) {
			t.Errorf("PKCS7Unpad = %v, want %v", unpadded, tt.data)
		}
	}
}

func TestPKCS7Unpad_Errors(t *testing.T) {
	// Empty data
	if _, err := PKCS7Unpad([]byte{}); err == nil {
		t.Error("expected error for empty data")
	}

	// Invalid padding value (too large)
	if _, err := PKCS7Unpad([]byte{1, 2, 3, 4, 16}); err == nil {
		t.Error("expected error for invalid padding value")
	}

	// Invalid padding bytes
	if _, err := PKCS7Unpad([]byte{1, 2, 3, 4, 1, 2}); err == nil {
		t.Error("expected error for invalid padding bytes")
	}
}
