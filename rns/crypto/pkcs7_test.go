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
	data := []byte("hello world")
	blockSize := 16
	padded := PKCS7Pad(data, blockSize)
	if len(padded)%blockSize != 0 {
		t.Errorf("PKCS7Pad padded length (%v) not a multiple of blockSize (%v)", len(padded), blockSize)
	}

	got, err := PKCS7Unpad(padded)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, data) {
		t.Errorf("PKCS7Unpad = %v, want %v", got, data)
	}
}
