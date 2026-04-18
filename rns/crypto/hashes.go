// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"crypto/sha256"
	"crypto/sha512"
)

// SHA256 returns the SHA-256 hash digest of [data].
func SHA256(data []byte) []byte {
	digest := sha256.New()
	digest.Write(data)
	return digest.Sum(nil)
}

// SHA512 returns the SHA-512 hash digest of [data].
func SHA512(data []byte) []byte {
	digest := sha512.New()
	digest.Write(data)
	return digest.Sum(nil)
}
