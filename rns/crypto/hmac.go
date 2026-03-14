// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
)

// HMAC calculates and returns the keyed-hash message authentication code using
// SHA-256 for the provided key and data. It ensures integrity and authenticity
// so receivers can detect tampering.
func HMAC(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
