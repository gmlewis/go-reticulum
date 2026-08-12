// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

func TestB256RepGolden(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", []byte{}, ""},
		{"zero", []byte{0x00}, "a"},
		{"ff", []byte{0xff}, "𐐏"},
		{"range16", bytes.Repeat([]byte{0}, 0), ""},
	}
	// range(16) -> "abcdefghijklmnop"
	var rb []byte
	for i := range 16 {
		rb = append(rb, byte(i))
	}
	cases = append(cases, struct {
		name string
		in   []byte
		want string
	}{"range16", rb, "abcdefghijklmnop"})

	for _, c := range cases {
		if got := B256Rep(c.in); got != c.want {
			t.Errorf("B256Rep(%v) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestB256RoundTrip(t *testing.T) {
	t.Parallel()
	var all []byte
	for i := range 256 {
		all = append(all, byte(i))
	}
	encoded := B256Rep(all)
	decoded, err := B256ToBytes(encoded)
	if err != nil {
		t.Fatalf("B256ToBytes round-trip failed: %v", err)
	}
	if !bytes.Equal(decoded, all) {
		t.Errorf("round-trip mismatch: got %d bytes, want %d", len(decoded), len(all))
	}
}

func TestB256ToBytesInvalid(t *testing.T) {
	t.Parallel()
	// 'w' is intentionally absent from the b256 alphabet (0x17 is 'x').
	if _, err := B256ToBytes("w"); err == nil {
		t.Fatal("B256ToBytes('w') = nil error, want error for unknown rune")
	}
}
