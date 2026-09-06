// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package cbor

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestDecoderEmpty(t *testing.T) {
	t.Parallel()

	dec := NewDecoder(bytes.NewReader(nil))
	v, err := dec.Decode()
	if v != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Decode() on empty reader = (%v, %v), want (nil, io.EOF)", v, err)
	}
}

func TestDecoderStreamingSequential(t *testing.T) {
	t.Parallel()

	buf := new(bytes.Buffer)

	// Encode multiple concatenated CBOR values of various types.
	val1 := "first message"
	val2 := int64(123456)
	val3 := true
	val4 := []string{"apple", "banana", "cherry"}
	val5 := map[string]any{
		"body": "streaming chat history",
		"ts":   int64(1700000000),
	}
	val6 := []byte{0xde, 0xad, 0xbe, 0xef}

	buf.Write(Encode(val1))
	buf.Write(Encode(val2))
	buf.Write(Encode(val3))
	buf.Write(Encode(val4))
	buf.Write(Encode(val5))
	buf.Write(Encode(val6))

	dec := NewDecoder(buf)

	// 1. String
	got1, err := dec.Decode()
	if err != nil || got1 != val1 {
		t.Fatalf("1. Decode() = (%v, %v), want (%q, nil)", got1, err, val1)
	}

	// 2. Int
	got2, err := dec.Decode()
	if err != nil || got2 != val2 {
		t.Fatalf("2. Decode() = (%v, %v), want (%v, nil)", got2, err, val2)
	}

	// 3. Bool
	got3, err := dec.Decode()
	if err != nil || got3 != val3 {
		t.Fatalf("3. Decode() = (%v, %v), want (%v, nil)", got3, err, val3)
	}

	// 4. []string (decodes as []any)
	got4, err := dec.Decode()
	if err != nil {
		t.Fatalf("4. Decode() error = %v", err)
	}
	arr4, ok := got4.([]any)
	if !ok || len(arr4) != 3 || arr4[0] != "apple" || arr4[1] != "banana" || arr4[2] != "cherry" {
		t.Fatalf("4. Decode() = %#v, want [apple, banana, cherry]", got4)
	}

	// 5. map[string]any (decodes as *Map)
	got5, err := dec.Decode()
	if err != nil {
		t.Fatalf("5. Decode() error = %v", err)
	}
	m5, ok := got5.(*Map)
	if !ok {
		t.Fatalf("5. Decode() type = %T, want *Map", got5)
	}
	if bVal, ok := m5.Get("body"); !ok || bVal != "streaming chat history" {
		t.Errorf("5. map body = (%v, %v), want %q", bVal, ok, "streaming chat history")
	}
	if tsVal, ok := m5.Get("ts"); !ok || tsVal != int64(1700000000) {
		t.Errorf("5. map ts = (%v, %v), want 1700000000", tsVal, ok)
	}

	// 6. []byte
	got6, err := dec.Decode()
	if err != nil || !bytes.Equal(got6.([]byte), val6) {
		t.Fatalf("6. Decode() = (%v, %v), want (%x, nil)", got6, err, val6)
	}

	// End of stream -> io.EOF
	gotEnd, err := dec.Decode()
	if gotEnd != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("End of stream Decode() = (%v, %v), want (nil, io.EOF)", gotEnd, err)
	}

	// Repeated call still returns io.EOF
	gotEnd2, err := dec.Decode()
	if gotEnd2 != nil || !errors.Is(err, io.EOF) {
		t.Fatalf("Subsequent Decode() = (%v, %v), want (nil, io.EOF)", gotEnd2, err)
	}
}

func TestDecoderTruncatedStream(t *testing.T) {
	t.Parallel()

	// Text string header indicating 20 bytes, but only 5 provided
	raw := []byte{0x74, 'h', 'e', 'l', 'l', 'o'}
	dec := NewDecoder(bytes.NewReader(raw))
	v, err := dec.Decode()
	if err == nil {
		t.Fatalf("Decode() on truncated input succeeded: %v, want error", v)
	}
	if !errors.Is(err, errTruncated) {
		t.Errorf("Decode() error = %v, want %v", err, errTruncated)
	}
}

func TestDecoderChatHistorySimulation(t *testing.T) {
	t.Parallel()

	// Simulate RRC chat history file where multiple entries are appended
	entries := []map[string]any{
		{"kind": "msg", "nick": "alice", "text": "hello from alice", "ts": int64(1001)},
		{"kind": "msg", "nick": "bob", "text": "hi alice!", "ts": int64(1002)},
		{"kind": "system", "nick": "", "text": "charlie joined", "ts": int64(1003)},
	}

	buf := new(bytes.Buffer)
	for _, entry := range entries {
		buf.Write(Encode(entry))
	}

	dec := NewDecoder(buf)
	var decoded []map[string]any
	for {
		v, err := dec.Decode()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("unexpected decode error: %v", err)
		}
		m, ok := v.(*Map)
		if !ok {
			t.Fatalf("decoded value is %T, want *Map", v)
		}
		decoded = append(decoded, m.ToStringMap())
	}

	if len(decoded) != len(entries) {
		t.Fatalf("decoded %d entries, want %d", len(decoded), len(entries))
	}

	for i, want := range entries {
		got := decoded[i]
		for k, wantVal := range want {
			if got[k] != wantVal {
				t.Errorf("entry[%d][%q] = %v, want %v", i, k, got[k], wantVal)
			}
		}
	}
}

func TestEncodeMapStringAny(t *testing.T) {
	t.Parallel()

	m := map[string]any{
		"z": int64(3),
		"a": "first",
		"m": []string{"sub1", "sub2"},
	}

	enc1 := Encode(m)
	enc2 := Encode(m)

	// Deterministic encoding
	if !bytes.Equal(enc1, enc2) {
		t.Fatal("Encode(map[string]any) is not deterministic")
	}

	// Verify decoding
	decVal, err := Decode(enc1)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	decMap, ok := decVal.(*Map)
	if !ok {
		t.Fatalf("decoded type = %T, want *Map", decVal)
	}

	// Keys should be ordered alphabetically: a, m, z
	pairs := decMap.Pairs()
	if len(pairs) != 3 {
		t.Fatalf("pair count = %v, want 3", len(pairs))
	}
	if pairs[0].Key != "a" || pairs[0].Val != "first" {
		t.Errorf("pair[0] = (%v, %v), want (a, first)", pairs[0].Key, pairs[0].Val)
	}
	if pairs[1].Key != "m" {
		t.Errorf("pair[1] key = %v, want m", pairs[1].Key)
	}
	if pairs[2].Key != "z" || pairs[2].Val != int64(3) {
		t.Errorf("pair[2] = (%v, %v), want (z, 3)", pairs[2].Key, pairs[2].Val)
	}
}

func TestEncodeMapAnyAny(t *testing.T) {
	t.Parallel()

	m := map[any]any{
		int64(2): "two",
		int64(1): "one",
		"text":   int64(99),
	}

	enc1 := Encode(m)
	enc2 := Encode(m)

	// Deterministic encoding
	if !bytes.Equal(enc1, enc2) {
		t.Fatal("Encode(map[any]any) is not deterministic")
	}

	decVal, err := Decode(enc1)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	decMap, ok := decVal.(*Map)
	if !ok {
		t.Fatalf("decoded type = %T, want *Map", decVal)
	}

	converted := decMap.ToMap()
	if converted[int64(1)] != "one" || converted[int64(2)] != "two" || converted["text"] != int64(99) {
		t.Errorf("ToMap() = %#v, unexpected contents", converted)
	}
}

func TestEncodeStringSlice(t *testing.T) {
	t.Parallel()

	slice := []string{"hello", "world"}
	enc := Encode(slice)

	v, err := Decode(enc)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("type = %T, want []any", v)
	}
	if len(arr) != 2 || arr[0] != "hello" || arr[1] != "world" {
		t.Errorf("decoded array = %#v, want [hello, world]", arr)
	}
}

func TestMapToStringMapAndToMap(t *testing.T) {
	t.Parallel()

	var nilMap *Map
	if nilMap.ToStringMap() != nil {
		t.Errorf("nilMap.ToStringMap() = %v, want nil", nilMap.ToStringMap())
	}
	if nilMap.ToMap() != nil {
		t.Errorf("nilMap.ToMap() = %v, want nil", nilMap.ToMap())
	}

	m := NewMap()
	m.Set("strKey", "strVal")
	m.Set(int64(42), "intKeyVal")

	strMap := m.ToStringMap()
	if strMap["strKey"] != "strVal" || strMap["42"] != "intKeyVal" {
		t.Errorf("ToStringMap() = %#v, want key conversions", strMap)
	}

	anyMap := m.ToMap()
	if anyMap["strKey"] != "strVal" || anyMap[int64(42)] != "intKeyVal" {
		t.Errorf("ToMap() = %#v, want exact key types", anyMap)
	}
}

func TestEncodeNilMapsAndSlices(t *testing.T) {
	t.Parallel()

	var (
		nilStrMap   map[string]any
		nilAnyMap   map[any]any
		nilStrSlice []string
	)

	encStrMap := Encode(nilStrMap)
	encAnyMap := Encode(nilAnyMap)
	encStrSlice := Encode(nilStrSlice)

	// Empty maps encode as 0xa0 (major 5, length 0)
	if !bytes.Equal(encStrMap, []byte{0xa0}) {
		t.Errorf("Encode(nil map[string]any) = %x, want a0", encStrMap)
	}
	if !bytes.Equal(encAnyMap, []byte{0xa0}) {
		t.Errorf("Encode(nil map[any]any) = %x, want a0", encAnyMap)
	}

	// Empty slice encodes as 0x80 (major 4, length 0)
	if !bytes.Equal(encStrSlice, []byte{0x80}) {
		t.Errorf("Encode(nil []string) = %x, want 80", encStrSlice)
	}
}

func TestEncodeVariousIntegerWidths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		val  any
		want int64
	}{
		{int8(10), 10},
		{int16(200), 200},
		{int32(50000), 50000},
		{uint(15), 15},
		{uint8(250), 250},
		{uint16(60000), 60000},
		{uint32(70000), 70000},
	}

	for _, tc := range tests {
		enc := Encode(tc.val)
		v, err := Decode(enc)
		if err != nil {
			t.Fatalf("Decode(%T(%v)) error: %v", tc.val, tc.val, err)
		}
		if !reflect.DeepEqual(v, tc.want) {
			t.Errorf("Decode(%T(%v)) = %v (%T), want %v (%T)", tc.val, tc.val, v, v, tc.want, tc.want)
		}
	}
}
