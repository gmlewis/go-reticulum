// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

import (
	"bytes"
	"math"
	"sort"
)

// Encode serializes v as non-canonical CBOR the way Python's cbor2.dumps
// does: minimal-length integer heads, text and byte strings distinguished,
// maps serialized in pair order, and float64 values always as the 8-byte
// 0xfb form (never 16/32-bit). Supported value types are nil, bool, int,
// int8..int64, uint..uint64, float32, float64, string, []byte, []any,
// []string, *Map, map[string]any, and map[any]any.
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
	case int8:
		return appendInt(b, int64(x))
	case int16:
		return appendInt(b, int64(x))
	case int32:
		return appendInt(b, int64(x))
	case int64:
		return appendInt(b, x)
	case uint:
		return appendUint(b, uint64(x))
	case uint8:
		return appendUint(b, uint64(x))
	case uint16:
		return appendUint(b, uint64(x))
	case uint32:
		return appendUint(b, uint64(x))
	case uint64:
		return appendUint(b, x)
	case float32:
		return appendFloat(b, float64(x))
	case float64:
		return appendFloat(b, x)
	case string:
		return appendText(b, x)
	case []byte:
		return appendByteString(b, x)
	case *Map:
		return appendMap(b, x)
	case map[string]any:
		return appendStringMap(b, x)
	case map[any]any:
		return appendAnyMap(b, x)
	case []any:
		return appendArray(b, x)
	case []string:
		return appendStringArray(b, x)
	case [][]byte:
		return appendByteSliceArray(b, x)
	case []int:
		return appendIntSlice(b, x)
	case []int64:
		return appendInt64Slice(b, x)
	case BigUint:
		return encodeBigUint(b, x)
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

func appendStringArray(b []byte, items []string) []byte {
	b = appendHead(b, 4, uint64(len(items)))
	for _, item := range items {
		b = appendText(b, item)
	}
	return b
}

func appendByteSliceArray(b []byte, items [][]byte) []byte {
	b = appendHead(b, 4, uint64(len(items)))
	for _, item := range items {
		b = appendByteString(b, item)
	}
	return b
}

func appendIntSlice(b []byte, items []int) []byte {
	b = appendHead(b, 4, uint64(len(items)))
	for _, item := range items {
		b = appendInt(b, int64(item))
	}
	return b
}

func appendInt64Slice(b []byte, items []int64) []byte {
	b = appendHead(b, 4, uint64(len(items)))
	for _, item := range items {
		b = appendInt(b, item)
	}
	return b
}

func appendStringMap(b []byte, m map[string]any) []byte {
	b = appendHead(b, 5, uint64(len(m)))
	if len(m) == 0 {
		return b
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b = appendText(b, k)
		b = appendValue(b, m[k])
	}
	return b
}

func appendAnyMap(b []byte, m map[any]any) []byte {
	b = appendHead(b, 5, uint64(len(m)))
	if len(m) == 0 {
		return b
	}
	type entry struct {
		keyBytes []byte
		val      any
	}
	entries := make([]entry, 0, len(m))
	for k, v := range m {
		kb := appendValue(nil, k)
		entries = append(entries, entry{keyBytes: kb, val: v})
	}
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].keyBytes) != len(entries[j].keyBytes) {
			return len(entries[i].keyBytes) < len(entries[j].keyBytes)
		}
		return bytes.Compare(entries[i].keyBytes, entries[j].keyBytes) < 0
	})
	for _, e := range entries {
		b = append(b, e.keyBytes...)
		b = appendValue(b, e.val)
	}
	return b
}
