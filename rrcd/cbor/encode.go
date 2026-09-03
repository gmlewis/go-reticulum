// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

import "math"

// Encode serializes v as non-canonical CBOR the way Python's cbor2.dumps
// does: minimal-length integer heads, text and byte strings distinguished,
// maps serialized in pair order, and float64 values always as the 8-byte
// 0xfb form (never 16/32-bit). Supported value types are nil, bool, int,
// int64, uint64, float64, string, []byte, []any, and *Map.
func Encode(v any) []byte {
	return appendValue(nil, v)
}

func appendValue(b []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(b, 0xf6)
	case bool:
		if x {
			return append(b, 0xf5)
		}
		return append(b, 0xf4)
	case int:
		return appendInt(b, int64(x))
	case int64:
		return appendInt(b, x)
	case uint64:
		return appendUint(b, x)
	case float64:
		return appendFloat(b, x)
	case string:
		return appendText(b, x)
	case []byte:
		return appendByteString(b, x)
	case *Map:
		return appendMap(b, x)
	case []any:
		return appendArray(b, x)
	}
	panic("cbor: unsupported value type")
}

func appendHead(b []byte, major byte, val uint64) []byte {
	switch {
	case val <= 23:
		return append(b, major<<5|byte(val))
	case val <= 0xff:
		return append(b, major<<5|24, byte(val))
	case val <= 0xffff:
		return append(b, major<<5|25, byte(val>>8), byte(val))
	case val <= 0xffffffff:
		return append(b, major<<5|26,
			byte(val>>24), byte(val>>16), byte(val>>8), byte(val))
	default:
		return append(b, major<<5|27,
			byte(val>>56), byte(val>>48), byte(val>>40), byte(val>>32),
			byte(val>>24), byte(val>>16), byte(val>>8), byte(val))
	}
}

func appendUint(b []byte, v uint64) []byte {
	return appendHead(b, 0, v)
}

func appendInt(b []byte, v int64) []byte {
	if v >= 0 {
		return appendHead(b, 0, uint64(v))
	}
	return appendHead(b, 1, uint64(-1-v))
}

func appendFloat(b []byte, f float64) []byte {
	b = append(b, 0xfb)
	bits := math.Float64bits(f)
	return append(b,
		byte(bits>>56), byte(bits>>48), byte(bits>>40), byte(bits>>32),
		byte(bits>>24), byte(bits>>16), byte(bits>>8), byte(bits))
}

func appendText(b []byte, s string) []byte {
	b = appendHead(b, 3, uint64(len(s)))
	return append(b, s...)
}

func appendByteString(b []byte, s []byte) []byte {
	b = appendHead(b, 2, uint64(len(s)))
	return append(b, s...)
}

func appendArray(b []byte, items []any) []byte {
	b = appendHead(b, 4, uint64(len(items)))
	for _, item := range items {
		b = appendValue(b, item)
	}
	return b
}

func appendMap(b []byte, m *Map) []byte {
	n := 0
	if m != nil {
		n = m.Len()
	}
	b = appendHead(b, 5, uint64(n))
	if m == nil {
		return b
	}
	for _, p := range m.Pairs() {
		b = appendValue(b, p.Key)
		b = appendValue(b, p.Val)
	}
	return b
}
