// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
)

// HMAC calculates and returns the keyed-hash message authentication code (HMAC) using SHA-256 for the provided data and key.
// It ensures cryptographic data integrity and authenticity, allowing receivers to verify that a payload has not been tampered with in transit.
func HMAC(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
