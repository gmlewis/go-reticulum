// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"errors"
)

var (
	// ErrInvalidPadding indicates that the provided byte slice does not conform to the strict PKCS#7 padding specification.
	// It is returned during decryption or unpadding operations when the padding bytes are missing, corrupt, or logically inconsistent.
	ErrInvalidPadding = errors.New("invalid pkcs7 padding")
)

// PKCS7Pad deterministically appends standard PKCS#7 padding to the end of a given byte slice.
// It computes the remaining block length and pads the block with the integer value of that length, ensuring the final slice aligns perfectly with the target cryptographic block size.
func PKCS7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padText...)
}

// PKCS7Unpad safely removes the PKCS#7 padding from a previously padded byte slice.
// It strictly validates the padding structure, checking that the trailing byte value correctly corresponds to the number of padding bytes and that each padding byte is uniform, returning the unpadded payload or an error if malformed.
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
