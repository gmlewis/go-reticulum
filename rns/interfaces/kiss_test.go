// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"testing"
)

func TestKISS(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"simple", []byte("hello")},
		{"with-fend", []byte{KISSFend, 0x01, KISSFend}},
		{"with-fesc", []byte{KISSFesc, 0x01, KISSFesc}},
		{"mixed", []byte{KISSFend, KISSFesc, 0xC0, 0xDB}},
		{"empty", []byte{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			escaped := KISSEscape(tt.raw)
			unescaped := KISSUnescape(escaped)
			if !bytes.Equal(tt.raw, unescaped) {
				t.Fatalf("KISS roundtrip mismatch for %q: got %x, want %x", tt.name, unescaped, tt.raw)
			}
		})
	}
}
