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
	ErrInvalidPadding = errors.New("invalid pkcs7 padding")
)

// PKCS7Pad appends PKCS7 padding to the data.
func PKCS7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - (len(data) % blockSize)
	padText := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padText...)
}

// PKCS7Unpad removes PKCS7 padding from the data.
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
