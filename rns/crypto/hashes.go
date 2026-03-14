// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/sha256"
	"crypto/sha512"
)

// SHA256 calculates and returns the standard SHA-256 cryptographic hash digest of the input data.
// It is utilized extensively across the network stack for producing uniform, collision-resistant checksums of arbitrary payloads.
func SHA256(data []byte) []byte {
	digest := sha256.New()
	digest.Write(data)
	return digest.Sum(nil)
}

// SHA512 calculates and returns the robust SHA-512 cryptographic hash digest of the input data.
// It provides a higher security margin and a wider bit-space, deployed in scenarios demanding maximum collision resistance.
func SHA512(data []byte) []byte {
	digest := sha512.New()
	digest.Write(data)
	return digest.Sum(nil)
}
