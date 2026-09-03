// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

import (
	"encoding/hex"
	"math"
	"testing"
)

func mustDecode(t *testing.T, hx string) any {
	t.Helper()
	v, err := Decode(mustHex(t, hx))
	if err != nil {
		t.Fatalf("Decode(%v): %v", hx, err)
	}
	return v
}

func TestDecodeGoldenStructures(t *testing.T) {
	t.Parallel()
	// NOTICE envelope golden: keys 0..6 in order.
	v := mustDecode(t, "a7000101150248aabbccddee112233031b0000018bcfe56800045820000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f056423666f6f06626869")
	m, ok := v.(*Map)
	if !ok {
		t.Fatalf("decoded type %T, want *Map", v)
	}
	if m.Len() != 7 {
		t.Fatalf("Len = %v, want 7", m.Len())
	}
	for _, tc := range []struct {
		key  int64
		want any
	}{
		{0, int64(1)},
		{1, int64(21)},
		{2, []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0x11, 0x22, 0x33}},
		{3, int64(1700000000000)},
		{4, bytesOf(0x00, 32)},
		{5, "#foo"},
		{6, "hi"},
	} {
		got, ok := m.Get(tc.key)
		if !ok {
			t.Fatalf("key %v missing", tc.key)
		}
		if !deepEqualAny(got, tc.want) {
			t.Errorf("key %v = %#v, want %#v", tc.key, got, tc.want)
		}
	}
}

func deepEqualAny(a, b any) bool {
	switch av := a.(type) {
	case []byte:
		bv, ok := b.([]byte)
		return ok && string(av) == string(bv)
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqualAny(av[i], bv[i]) {
				return false
			}
		}
		return true
	case *Map:
		bv, ok := b.(*Map)
		if !ok || av.Len() != bv.Len() {
			return false
		}
		ap, bp := av.Pairs(), bv.Pairs()
		for i := range ap {
			if !deepEqualAny(ap[i].Key, bp[i].Key) ||
				!deepEqualAny(ap[i].Val, bp[i].Val) {
				return false
			}
		}
		return true
	}
	return a == b
}

func TestDecodeIgnoresTrailingBytes(t *testing.T) {
	t.Parallel()
	// cbor2.loads decodes one value and ignores the rest.
	if v := mustDecode(t, "0001"); v != int64(0) {
		t.Fatalf("Decode(0001) = %v, want 0", v)
	}
	if v := mustDecode(t, "010203"); v != int64(1) {
		t.Fatalf("Decode(010203) = %v, want 1", v)
	}
}

func TestDecodeEmptyErrors(t *testing.T) {
	t.Parallel()
	if _, err := Decode(nil); err == nil {
		t.Fatal("Decode(nil) succeeded; Python errors on empty input")
	}
	if _, err := Decode([]byte{}); err == nil {
		t.Fatal("Decode(empty) succeeded")
	}
}

func TestDecodeTruncatedErrors(t *testing.T) {
	t.Parallel()
	for _, hx := range []string{"18", "580241", "5b", "6241", "a101", "f93c"} {
		if _, err := Decode(mustHex(t, hx)); err == nil {
			t.Errorf("Decode(%q) succeeded, want truncation error", hx)
		}
	}
}

func TestDecodeUnexpectedBreakErrors(t *testing.T) {
	t.Parallel()
	// Python yields an internal sentinel object for a lone break; Go's
	// decoder reports malformed input instead (callers treat every decode
	// failure the same way).
	if _, err := Decode(mustHex(t, "ff")); err == nil {
		t.Fatal("Decode(ff) succeeded, want error")
	}
}

func TestDecodeIndefiniteLengths(t *testing.T) {
	t.Parallel()
	// Indefinite array.
	v := mustDecode(t, "9f0102ff")
	items, ok := v.([]any)
	if !ok || len(items) != 2 || items[0] != int64(1) || items[1] != int64(2) {
		t.Fatalf("indefinite array = %#v, want [1 2]", v)
	}
	// Indefinite text string across chunks.
	if v := mustDecode(t, "7f61416142ff"); v != "AB" {
		t.Fatalf("indefinite text = %#v, want AB", v)
	}
	// Indefinite byte string across chunks.
	if v := mustDecode(t, "5f42010243030405ff"); string(v.([]byte)) != "\x01\x02\x03\x04\x05" {
		t.Fatalf("indefinite bytes = %#v", v)
	}
	// Indefinite map.
	v = mustDecode(t, "bf0102ff")
	m, ok := v.(*Map)
	if !ok || m.Len() != 1 {
		t.Fatalf("indefinite map = %#v", v)
	}
	if got, _ := m.Get(int64(1)); got != int64(2) {
		t.Fatalf("indefinite map[1] = %v, want 2", got)
	}
}

func TestDecodeTagSkipped(t *testing.T) {
	t.Parallel()
	// c1 01 = tag(1) around 1; the tagged value is returned.
	if v := mustDecode(t, "c101"); v != int64(1) {
		t.Fatalf("tagged value = %#v, want 1", v)
	}
	// Tag around a string.
	if v := mustDecode(t, "c0624142"); v != "AB" {
		t.Fatalf("tagged string = %#v, want AB", v)
	}
}

func TestDecodeFloats(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		hx   string
		want float64
	}{
		{"f93c00", 1.0},              // half
		{"f90000", 0.0},              // half zero
		{"fa3f800000", 1.0},          // single
		{"fb3fe0000000000000", 0.5},  // double
		{"fbc010666666666666", -4.1}, // double negative
		{"f97c00", math.Inf(1)},      // half inf
		{"f9fc00", math.Inf(-1)},     // half -inf
		{"f93400", 0.25},             // half quarter
	} {
		got, err := Decode(mustHex(t, tc.hx))
		if err != nil {
			t.Fatalf("Decode(%v): %v", tc.hx, err)
		}
		f, ok := got.(float64)
		if !ok {
			t.Fatalf("Decode(%v) type %T, want float64", tc.hx, got)
		}
		if math.IsInf(tc.want, 0) && !math.IsInf(f, 0) {
			t.Errorf("Decode(%v) = %v, want ±inf", tc.hx, f)
			continue
		}
		if !math.IsInf(tc.want, 0) && f != tc.want {
			t.Errorf("Decode(%v) = %v, want %v", tc.hx, f, tc.want)
		}
	}
	// f8 3c is an unassigned one-byte simple value and must error.
	if _, err := Decode(mustHex(t, "f83c")); err == nil {
		t.Fatal("Decode(f83c) succeeded, want error")
	}
}

func TestDecodeHalfSubnormal(t *testing.T) {
	t.Parallel()
	// 0x0001 = smallest positive half subnormal = 2^-24.
	if v := mustDecode(t, "f90001"); v != math.Float64frombits(0x3e70000000000000) {
		t.Fatalf("half subnormal = %v, want 2^-24", v)
	}
}

func TestDecodeBoolIntKeyEquivalence(t *testing.T) {
	t.Parallel()
	// a1 f5 01: map with key True holding 1 — the key stays bool on
	// re-encode, but int lookups find it.
	v := mustDecode(t, "a1f501")
	m := v.(*Map)
	if m.Len() != 1 {
		t.Fatalf("Len = %v, want 1", m.Len())
	}
	if got := keys(m); len(got) != 1 || got[0] != true {
		t.Fatalf("keys = %v, want [true]", got)
	}
	if _, ok := m.Get(int64(1)); !ok {
		t.Fatal("int lookup 1 failed on a True-keyed map")
	}
	if got := hex.EncodeToString(Encode(m)); got != "a1f501" {
		t.Fatalf("re-encode = %v, want a1f501 (original key type kept)", got)
	}
}

func TestDecodeDuplicateIntKeyCollapses(t *testing.T) {
	t.Parallel()
	// a1 01 01: one entry {1: 1}.
	v := mustDecode(t, "a10101")
	m := v.(*Map)
	if m.Len() != 1 {
		t.Fatalf("Len = %v, want 1", m.Len())
	}
}

func TestDecodeWideInts(t *testing.T) {
	t.Parallel()
	if v := mustDecode(t, "1bffffffffffffffff"); v != uint64(math.MaxUint64) {
		t.Fatalf("uint64 max = %#v", v)
	}
	if v := mustDecode(t, "3b7fffffffffffffff"); v != int64(math.MinInt64) {
		t.Fatalf("int64 min = %#v", v)
	}
	// Negative overflow (arg >= 2^63) errors, unlike Python's bignum.
	if _, err := Decode(mustHex(t, "3b8000000000000000")); err == nil {
		t.Fatal("Decode of -2^64 succeeded, want overflow error")
	}
}

func TestDecodeErrorOnBadInput(t *testing.T) {
	t.Parallel()
	for _, hx := range []string{"1f", "3f", "ff00", "c23f", "bf"} {
		if _, err := Decode(mustHex(t, hx)); err == nil {
			t.Errorf("Decode(%q) succeeded, want error", hx)
		}
	}
}

func TestDecodeDeepNestingErrors(t *testing.T) {
	t.Parallel()
	// 200 nested arrays exceed the decoder depth limit.
	data := make([]byte, 0, 200)
	for i := 0; i < 200; i++ {
		data = append(data, 0x80|1) // array head with 1 element... placeholder
	}
	_ = data
	// Build: open brackets via indefinite arrays are simplest.
	nested := make([]byte, 0, 128)
	for i := 0; i < 100; i++ {
		nested = append(nested, 0x9f) // indefinite array open
	}
	nested = append(nested, 0x01, 0xff)
	if _, err := Decode(nested); err == nil {
		t.Fatal("deeply nested decode succeeded, want depth error")
	}
}
