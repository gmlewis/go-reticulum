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

func TestUnpackPreserveBinMapKeyOrder(t *testing.T) {
	t.Parallel()

	raw := []byte{
		0x82,
		0xc4, 0x02, 'a', 'a', 0x01,
		0xc4, 0x02, 'b', 'b', 0x02,
	}

	unpacked, err := UnpackPreserveBinMapKeyOrder(raw)
	if err != nil {
		t.Fatalf("UnpackPreserveBinMapKeyOrder() error = %v", err)
	}

	ordered, ok := unpacked.(OrderedMap)
	if !ok {
		t.Fatalf("unpacked type = %T, want OrderedMap", unpacked)
	}
	if len(ordered) != 2 {
		t.Fatalf("len(ordered) = %v, want 2", len(ordered))
	}
	if got := reflect.TypeOf(ordered[0].Key); got == nil || got.Name() != "binaryMapKey" {
		t.Fatalf("first key type = %T, want binaryMapKey", ordered[0].Key)
	}
	if got := reflect.TypeOf(ordered[1].Key); got == nil || got.Name() != "binaryMapKey" {
		t.Fatalf("second key type = %T, want binaryMapKey", ordered[1].Key)
	}
	if got := reflect.ValueOf(ordered[0].Key).String(); got != "aa" {
		t.Fatalf("first key = %q, want %q", got, "aa")
	}
	if got := reflect.ValueOf(ordered[1].Key).String(); got != "bb" {
		t.Fatalf("second key = %q, want %q", got, "bb")
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

// toInt64 normalizes any signed or unsigned Go integer to int64 for value
// comparison after a msgpack round-trip, which may widen the type (e.g. uint
// -> uint64).
func toInt64(v any) (int64, bool) {
	if isSignedInt(v) {
		return reflect.ValueOf(v).Int(), true
	}
	if isUnsignedInt(v) {
		return int64(reflect.ValueOf(v).Uint()), true
	}
	return 0, false
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

// TestOrderedMapMarshalInsertionOrder pins MarshalMsgpack: an OrderedMap packs
// as a msgpack map whose entries appear in slice (insertion) order, byte-for-byte
// matching Python umsgpack's serialization of an equivalently-ordered dict — the
// Go map path iterates keys in random order, so this is the only way to get
// stable, Python-parity bytes for a multi-key map.
func TestOrderedMapMarshalInsertionOrder(t *testing.T) {
	t.Parallel()
	m := OrderedMap{
		{Key: "display_name", Value: "Test Peer"},
		{Key: "announce_interval", Value: uint(720)},
		{Key: "last_announce", Value: 1700000123.456},
		{Key: "node_last_announce", Value: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}},
		{Key: "propagation_node", Value: []byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00}},
		{Key: "last_lxmf_sync", Value: uint(1700000200)},
		{Key: "node_connects", Value: uint(42)},
		{Key: "served_page_requests", Value: uint(100)},
		{Key: "served_file_requests", Value: uint(7)},
	}
	// Expected bytes (Python umsgpack golden): fixmap 9, fixstr keys in
	// insertion order, uint16 (cd) for 720, float64 (cb) for last_announce,
	// bin8 (c4) for the two byte slices, uint32 (ce) for 1700000200, and
	// positive fixints for 42/100/7.
	want := []byte{
		0x89,
		0xac, 'd', 'i', 's', 'p', 'l', 'a', 'y', '_', 'n', 'a', 'm', 'e',
		0xa9, 'T', 'e', 's', 't', ' ', 'P', 'e', 'e', 'r',
		0xb1, 'a', 'n', 'n', 'o', 'u', 'n', 'c', 'e', '_', 'i', 'n', 't', 'e', 'r', 'v', 'a', 'l',
		0xcd, 0x02, 0xd0,
		0xad, 'l', 'a', 's', 't', '_', 'a', 'n', 'n', 'o', 'u', 'n', 'c', 'e',
		0xcb, 0x41, 0xd9, 0x54, 0xfc, 0x5e, 0xdd, 0x2f, 0x1b,
		0xb2, 'n', 'o', 'd', 'e', '_', 'l', 'a', 's', 't', '_', 'a', 'n', 'n', 'o', 'u', 'n', 'c', 'e',
		0xc4, 0x10, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		0xb0, 'p', 'r', 'o', 'p', 'a', 'g', 'a', 't', 'i', 'o', 'n', '_', 'n', 'o', 'd', 'e',
		0xc4, 0x10, 0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00,
		0xae, 'l', 'a', 's', 't', '_', 'l', 'x', 'm', 'f', '_', 's', 'y', 'n', 'c',
		0xce, 0x65, 0x53, 0xf1, 0xc8,
		0xad, 'n', 'o', 'd', 'e', '_', 'c', 'o', 'n', 'n', 'e', 'c', 't', 's',
		0x2a,
		0xb4, 's', 'e', 'r', 'v', 'e', 'd', '_', 'p', 'a', 'g', 'e', '_', 'r', 'e', 'q', 'u', 'e', 's', 't', 's',
		0x64,
		0xb4, 's', 'e', 'r', 'v', 'e', 'd', '_', 'f', 'i', 'l', 'e', '_', 'r', 'e', 'q', 'u', 'e', 's', 't', 's',
		0x07,
	}

	got, err := Pack(m)
	if err != nil {
		t.Fatalf("Pack(OrderedMap) error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Pack(OrderedMap) bytes diverge from Python umsgpack golden:\n got %x\n want %x", got, want)
	}

	// Round-trip through an order-preserving unpack: same keys, same order,
	// same values.
	rt, err := UnpackPreserveBinMapKeyOrder(got)
	if err != nil {
		t.Fatalf("UnpackPreserveBinMapKeyOrder error = %v", err)
	}
	om, ok := rt.(OrderedMap)
	if !ok {
		t.Fatalf("unpacked type = %T, want OrderedMap", rt)
	}
	if len(om) != len(m) {
		t.Fatalf("round-trip len = %v, want %v", len(om), len(m))
	}
	for i, e := range m {
		got := om[i]
		if got.Key != e.Key {
			t.Errorf("entry %v key = %v, want %v (insertion order not preserved)", i, got.Key, e.Key)
		}
		if gb, ok := e.Value.([]byte); ok {
			if !bytes.Equal(got.Value.([]byte), gb) {
				t.Errorf("entry %v (%v) bytes mismatch", i, e.Key)
			}
		} else if fv, ok := e.Value.(float64); ok {
			if got.Value.(float64) != fv {
				t.Errorf("entry %v (%v) value = %v, want %v", i, e.Key, got.Value, e.Value)
			}
		} else {
			// Integers: umsgpack round-trips unsigned Go ints as uint64 and
			// small positives as int64; compare by int64 value.
			gotI, _ := toInt64(got.Value)
			wantI, _ := toInt64(e.Value)
			if gotI != wantI {
				t.Errorf("entry %v (%v) value = %v, want %v", i, e.Key, got.Value, e.Value)
			}
		}
	}
}

func TestOrderedMapGetSet(t *testing.T) {
	t.Parallel()
	m := OrderedMap{
		{Key: "a", Value: uint(1)},
		{Key: "b", Value: "two"},
		{Key: 3, Value: uint(4)},
	}

	// Get finds existing keys (string and non-string).
	if v, ok := m.Get("a"); !ok || toInt64Must(v) != 1 {
		t.Fatalf(`Get("a") = %v,%v, want 1,true`, v, ok)
	}
	if v, ok := m.Get("b"); !ok || v != "two" {
		t.Fatalf(`Get("b") = %v,%v, want "two",true`, v, ok)
	}
	if v, ok := m.Get(3); !ok || toInt64Must(v) != 4 {
		t.Fatalf(`Get(3) = %v,%v, want 4,true`, v, ok)
	}
	if _, ok := m.Get("missing"); ok {
		t.Fatalf(`Get("missing") = true, want false`)
	}

	// Set on an existing key updates the value in place, preserving position
	// (Python dict semantics: re-assigning a key keeps its place).
	m = m.Set("b", "TWO")
	if v, _ := m.Get("b"); v != "TWO" {
		t.Fatalf(`Set("b") value = %v, want "TWO"`, v)
	}
	if m[1].Key != "b" || m[1].Value != "TWO" {
		t.Fatalf("Set updated entry in wrong position: %+v", m[1])
	}
	if len(m) != 3 {
		t.Fatalf("Set existing changed length to %v, want 3", len(m))
	}

	// Set on a new key appends.
	m = m.Set("c", uint(5))
	if v, _ := m.Get("c"); toInt64Must(v) != 5 {
		t.Fatalf(`Set("c") value = %v, want 5`, v)
	}
	if len(m) != 4 || m[3].Key != "c" {
		t.Fatalf("Set new did not append: len=%v last=%+v", len(m), m[3])
	}
}

func toInt64Must(v any) int64 {
	i, _ := toInt64(v)
	return i
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
