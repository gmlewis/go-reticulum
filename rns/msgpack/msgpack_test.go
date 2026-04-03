// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package msgpack

import (
	"bytes"
	"math"
	"reflect"
	"testing"
)

func TestMsgPack(t *testing.T) {
	t.Parallel()
	testCases := []any{
		nil,
		true,
		false,
		int64(123),
		int64(-123),
		float64(1.23),
		"hello world",
		[]byte{1, 2, 3},
		[]any{int64(1), "two", true},
		map[any]any{"key": int64(42), int64(1): "value"},
	}

	for _, tc := range testCases {
		packed, err := Pack(tc)
		if err != nil {
			t.Errorf("Pack failed for %v: %v", tc, err)
			continue
		}

		unpacked, err := Unpack(packed)
		if err != nil {
			t.Errorf("Unpack failed for %v: %v", tc, err)
			continue
		}

		if !reflect.DeepEqual(tc, unpacked) {
			t.Errorf("expected %v, got %v", tc, unpacked)
		}
	}
}

func TestMsgPackTypes(t *testing.T) {
	t.Parallel()
	// Special case for byte slices which are unpacked as []byte
	b := []byte{1, 2, 3}
	packed, _ := Pack(b)
	unpacked, _ := Unpack(packed)
	if !bytes.Equal(b, unpacked.([]byte)) {
		t.Errorf("expected %v, got %v", b, unpacked)
	}
}

func TestPackUnpackExtended(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		val  any
	}{
		{"nil", nil},
		{"true", true},
		{"false", false},

		// Integers
		{"posFixInt 0", int64(0)},
		{"posFixInt 127", int64(127)},
		{"negFixInt -1", int64(-1)},
		{"negFixInt -32", int64(-32)},
		{"int8 min", int64(math.MinInt8)},
		{"int8 max", int64(math.MaxInt8)},
		{"int16 min", int64(math.MinInt16)},
		{"int16 max", int64(math.MaxInt16)},
		{"int32 min", int64(math.MinInt32)},
		{"int32 max", int64(math.MaxInt32)},
		{"int64 min", int64(math.MinInt64)},
		{"int64 max", int64(math.MaxInt64)},

		{"uint8 min", uint64(0)},
		{"uint8 max", uint64(math.MaxUint8)},
		{"uint16 max", uint64(math.MaxUint16)},
		{"uint32 max", uint64(math.MaxUint32)},
		{"uint64 max", uint64(math.MaxUint64)},

		// Floats
		{"float32", float32(1.234)},
		{"float64", float64(1.23456789)},
		{"float64 max", math.MaxFloat64},

		// Strings
		{"empty string", ""},
		{"fixStr short", "hello"},
		// Strings boundaries
		{"fixStr 31", makeString(31)},
		{"str8 32", makeString(32)},
		{"str8 255", makeString(255)},
		{"str16 256", makeString(256)},
		{"str16 65535", makeString(65535)},

		// Binary boundaries
		{"bin8 255", makeBytes(255)},
		{"bin16 256", makeBytes(256)},
		{"bin16 65535", makeBytes(65535)},

		// Array boundaries
		{"fixArray 15", makeArray(15)},
		{"array16 16", makeArray(16)},

		// Map boundaries
		{"fixMap 15", makeMap(15)},
		{"map16 16", makeMap(16)},

		// Pointers
		{"int pointer", pointerToInt(42)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			packed, err := Pack(tt.val)
			if err != nil {
				t.Fatalf("Pack() error = %v", err)
			}

			unpacked, err := Unpack(packed)
			if err != nil {
				t.Fatalf("Unpack() error = %v", err)
			}

			// Special handling for expected values after roundtrip
			expected := tt.val
			if expected != nil {
				rv := reflect.ValueOf(expected)
				if rv.Kind() == reflect.Ptr {
					expected = rv.Elem().Interface()
				}
				// Normalize integers for comparison
				if isSignedInt(expected) {
					expected = reflect.ValueOf(expected).Int()
				}
				if isUnsignedInt(expected) {
					// MessagePack doesn't distinguish between signed and unsigned for small positive values.
					// Unpack returns int64 for small positive values.
					uv := reflect.ValueOf(expected).Uint()
					if uv <= 127 {
						expected = int64(uv)
					} else {
						expected = uv
					}
				}
				// Special handling for slices and maps
				if rv.Kind() == reflect.Slice {
					// If it's a slice of int (e.g., []any{1, 2, 3}), they are unpacked as int64.
					if rv.Type().Elem().Kind() == reflect.Interface {
						l := rv.Len()
						na := make([]any, l)
						for i := 0; i < l; i++ {
							v := rv.Index(i).Interface()
							if isSignedInt(v) {
								na[i] = reflect.ValueOf(v).Int()
							} else {
								na[i] = v
							}
						}
						expected = na
					}
				}
				if rv.Kind() == reflect.Map {
					// Normalize map values if they are integers
					nm := make(map[any]any, rv.Len())
					for _, k := range rv.MapKeys() {
						kv := k.Interface()
						rk := k
						if rk.Kind() == reflect.Interface {
							rk = rk.Elem()
						}
						if isSignedInt(rk.Interface()) {
							kv = rk.Int()
						}

						vv := rv.MapIndex(k).Interface()
						rvv := rv.MapIndex(k)
						if rvv.Kind() == reflect.Interface {
							rvv = rvv.Elem()
						}
						if rvv.IsValid() && isSignedInt(rvv.Interface()) {
							vv = rvv.Int()
						}
						nm[kv] = vv
					}
					expected = nm
				}
			}

			if !reflect.DeepEqual(unpacked, expected) {
				t.Errorf("Unpack() = %v (%T), want %v (%T)", unpacked, unpacked, expected, expected)
			}
		})
	}
}

func makeString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

func makeBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 256)
	}
	return b
}

func makeArray(n int) []any {
	a := make([]any, n)
	for i := range a {
		a[i] = int64(i)
	}
	return a
}

func makeMap(n int) map[any]any {
	m := make(map[any]any, n)
	for i := 0; i < n; i++ {
		m[int64(i)] = int64(i * 2)
	}
	return m
}

func pointerToInt(i int) *int {
	return &i
}

func isSignedInt(v any) bool {
	k := reflect.TypeOf(v).Kind()
	return k >= reflect.Int && k <= reflect.Int64
}

func isUnsignedInt(v any) bool {
	k := reflect.TypeOf(v).Kind()
	return k >= reflect.Uint && k <= reflect.Uint64
}

func TestUnpackMalformed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"truncated bin8", []byte{bin8, 5, 1, 2}},
		{"truncated str16", []byte{str16, 0, 10, 'a', 'b'}},
		{"unknown type", []byte{0xc1}}, // 0xc1 is never used in msgpack
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Unpack(tt.data)
			if err == nil {
				t.Error("Unpack() expected error for malformed data, got nil")
			}
		})
	}
}

func TestPackUnsupported(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		val  any
	}{
		{"chan", make(chan int)},
		{"func", func() {}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Pack(tt.val)
			if err == nil {
				t.Error("Pack() expected error for unsupported type, got nil")
			}
		})
	}
}

func TestMapWithByteSliceKey(t *testing.T) {
	t.Parallel()
	// We can't create a map[[]byte]any in Go.
	// But we can simulate what happens if we UNPACK a map that has binary keys.
	data := []byte{
		0x81,                      // fixMap of 1
		0xc4, 0x03, 'k', 'e', 'y', // bin8 "key"
		0xa5, 'v', 'a', 'l', 'u', 'e', // fixStr "value"
	}

	unpacked, err := Unpack(data)
	if err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}

	um := unpacked.(map[any]any)
	if _, ok := um["key"]; !ok {
		t.Errorf("Expected key 'key' (string) in unpacked map, got %v", um)
	}
}
