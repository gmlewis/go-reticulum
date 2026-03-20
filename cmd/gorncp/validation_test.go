// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestValidateIdentityHash(t *testing.T) {
	t.Parallel()

	destLen := (rns.TruncatedHashLength / 8) * 2

	tests := []struct {
		name    string
		hash    string
		wantErr bool
	}{
		{"too short", "abc123", true},
		{"valid lowercase", strings.Repeat("a", destLen), false},
		{"valid uppercase", strings.Repeat("A", destLen), false},
		{"invalid hex", strings.Repeat("g", destLen), true},
		{"empty", "", true},
		{"exact length", "0123456789abcdef0123456789abcdef", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIdentityHash(tt.hash)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateIdentityHash(%q) error = %v, wantErr %v", tt.hash, err, tt.wantErr)
			}
		})
	}
}
