// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"testing"
)

func TestKISSEscapeMatchesPython(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  []byte
		want []byte
	}{
		{name: "empty", raw: []byte{}, want: []byte{}},
		{name: "plain-text", raw: []byte("hello"), want: []byte("hello")},
		{name: "fend-only", raw: []byte{0xc0}, want: []byte{0xdb, 0xdc}},
		{name: "fesc-only", raw: []byte{0xdb}, want: []byte{0xdb, 0xdd}},
		{name: "fend-then-fesc", raw: []byte{0xc0, 0xdb}, want: []byte{0xdb, 0xdc, 0xdb, 0xdd}},
		{name: "fesc-then-fend", raw: []byte{0xdb, 0xc0}, want: []byte{0xdb, 0xdd, 0xdb, 0xdc}},
		{name: "alternating", raw: []byte{0xc0, 0xdb, 0xc0, 0xdb}, want: []byte{0xdb, 0xdc, 0xdb, 0xdd, 0xdb, 0xdc, 0xdb, 0xdd}},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := kissEscape(tt.raw)
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("escaped output mismatch:\n got: %x\nwant: %x", got, tt.want)
			}
		})
	}
}
