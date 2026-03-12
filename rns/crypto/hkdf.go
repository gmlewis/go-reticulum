// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"math"
)

const (
	hkdfHashLen = 32
)

// HKDF implements the HKDF-SHA256 key derivation function as used in Reticulum.
func HKDF(length int, deriveFrom, salt, context []byte) ([]byte, error) {
	if length < 1 {
		return nil, errors.New("invalid output key length")
	}

	if len(deriveFrom) == 0 {
		return nil, errors.New("cannot derive key from empty input material")
	}

	if len(salt) == 0 {
		salt = make([]byte, hkdfHashLen)
	}

	if context == nil {
		context = []byte{}
	}

	// Extract
	h := hmac.New(sha256.New, salt)
	h.Write(deriveFrom)
	pseudoRandomKey := h.Sum(nil)

	// Expand
	numBlocks := int(math.Ceil(float64(length) / float64(hkdfHashLen)))
	var block []byte
	var derived []byte

	for i := range numBlocks {
		h := hmac.New(sha256.New, pseudoRandomKey)
		h.Write(block)
		h.Write(context)
		h.Write([]byte{byte((i + 1) % 256)})
		block = h.Sum(nil)
		derived = append(derived, block...)
	}

	return derived[:length], nil
}
