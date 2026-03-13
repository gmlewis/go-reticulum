// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package interfaces

import (
	"bytes"
	"testing"
)

func TestHDLC(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{"simple", []byte("hello")},
		{"with-flags", []byte{HDLCFlag, 0x01, HDLCFlag}},
		{"with-escapes", []byte{HDLCEsc, 0x01, HDLCEsc}},
		{"mixed", []byte{HDLCFlag, HDLCEsc, 0x7E, 0x7D}},
		{"empty", []byte{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			escaped := HDLCEscape(tt.raw)
			unescaped := HDLCUnescape(escaped)
			if !bytes.Equal(tt.raw, unescaped) {
				t.Fatalf("HDLC roundtrip mismatch for %q: got %x, want %x", tt.name, unescaped, tt.raw)
			}
		})
	}
}
