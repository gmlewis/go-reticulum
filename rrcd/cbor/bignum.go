// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

import "math/big"

// BigUint is a decoded CBOR bignum: a tag-2 (positive) or tag-3
// (negative) value whose magnitude is carried as the raw big-endian
// payload bytes. Python decodes both tags into its unbounded int; Go
// keeps the sign and the magnitude so the value can compare and render.
// For the negative tag the magnitude is the CBOR-encoded -value - 1 per
// RFC 8949, so re-encoding round-trips byte-identically.
type BigUint struct {
	Negative  bool
	Magnitude []byte
}

// String renders the decimal value the way Python's str() does for the
// decoded int, including the minus sign for the negative tag.
func (b BigUint) String() string {
	n := new(big.Int).SetBytes(b.Magnitude)
	if b.Negative {
		n.Sub(n, big.NewInt(1))
		n.Neg(n)
	}
	return n.String()
}

// AsUint64 reports the magnitude as a uint64 when it fits.
func (b BigUint) AsUint64() (uint64, bool) {
	if len(b.Magnitude) > 8 {
		return 0, false
	}
	var n uint64
	for _, byt := range b.Magnitude {
		n = n<<8 | uint64(byt)
	}
	return n, true
}

// trimMagnitude drops the leading zero bytes a padded payload may carry.
func trimMagnitude(b []byte) []byte {
	i := 0
	for i < len(b)-1 && b[i] == 0 {
		i++
	}
	return b[i:]
}

// IsZero reports whether the magnitude is zero.
func (b BigUint) IsZero() bool {
	for _, byt := range b.Magnitude {
		if byt != 0 {
			return false
		}
	}
	return true
}

// encodeBigUint appends the bignum tag form the way cbor2 re-encodes a
// wide Python int: the plain integer form when the value fits, else the
// RFC 8949 tag with the minimal-length magnitude byte string.
func encodeBigUint(b []byte, v BigUint) []byte {
	mag := trimMagnitude(v.Magnitude)
	tag := uint64(2)
	if v.Negative {
		tag = 3
	}
	if len(mag) <= 8 {
		if n, ok := (BigUint{Negative: v.Negative, Magnitude: mag}).AsUint64(); ok {
			if v.Negative {
				if n <= 1<<63-1 {
					return appendInt(b, -1-int64(n))
				}
			} else {
				return appendUint(b, n)
			}
		}
	}
	b = appendHead(b, 6, tag)
	return appendByteString(b, mag)
}
