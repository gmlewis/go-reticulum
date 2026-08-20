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
	}{
		{name: "empty", raw: []byte{}},
		{name: "plain-text", raw: []byte("hello")},
		{name: "fend-only", raw: []byte{0xc0}},
		{name: "fesc-only", raw: []byte{0xdb}},
		{name: "fend-then-fesc", raw: []byte{0xc0, 0xdb}},
		{name: "fesc-then-fend", raw: []byte{0xdb, 0xc0}},
		{name: "alternating", raw: []byte{0xc0, 0xdb, 0xc0, 0xdb}},
	}

	raws := make([][]byte, len(tests))
	for i, tt := range tests {
		raws[i] = tt.raw
	}
	wants := pythonKissEscape(t, raws)

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := kissEscape(tt.raw)
			if !bytes.Equal(got, wants[i]) {
				t.Fatalf("escaped output mismatch vs live Python:\n got: %x\nwant: %x", got, wants[i])
			}
		})
	}
}
