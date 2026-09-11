// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"errors"
)

var (
	// ErrInvalidPadding reports malformed PKCS#7 padding.
	ErrInvalidPadding = errors.New("invalid pkcs7 padding")
)

// PKCS7Pad returns a new buffer with PKCS#7 padding so len(result) is a
// multiple of blockSize. It never writes into data's backing array: a naive
// append would reuse spare capacity and race when concurrent Encrypt calls
// share a slice (CI data race in Link.Teardown → Token.Encrypt).
func PKCS7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

// PKCS7Unpad removes PKCS#7 padding and validates that it is well formed.
func PKCS7Unpad(data []byte) ([]byte, error) {
	length := len(data)
	if length == 0 {
		return nil, ErrInvalidPadding
	}
	padding := int(data[length-1])
	if padding == 0 || padding > length {
		return nil, ErrInvalidPadding
	}
	for i := range padding {
		if data[length-1-i] != byte(padding) {
			return nil, ErrInvalidPadding
		}
	}
	return data[:length-padding], nil
}
