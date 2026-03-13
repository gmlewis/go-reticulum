// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package crypto

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestHMAC(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  string
		data string
		want string
	}{
		{
			name: "empty key and data",
			key:  "",
			data: "",
			want: "b613679a0814d9ec772f95d778c35fc5ff1697c493715653c6c712144292c5ad",
		},
		{
			name: "short key and data",
			key:  "key",
			data: "The quick brown fox jumps over the lazy dog",
			want: "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
		},
		{
			name: "longer key",
			key:  "0123456789012345678901234567890123456789012345678901234567890123456789",
			data: "data",
			want: "2c6b2aaa892a50cdf6e9039a5b1b1463182326d4967b65b70778b4a96b9f99fc",
		},
		{
			name: "exactly 64-byte key",
			key:  "0123456789012345678901234567890123456789012345678901234567890123",
			data: "data",
			want: "c6b49c2c19941329efb1b08c7efdbde63509a3ec949d6eefdf391787f5e02b5d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			key := []byte(tt.key)
			data := []byte(tt.data)
			want, _ := hex.DecodeString(tt.want)
			got := HMAC(key, data)
			if !bytes.Equal(got, want) {
				t.Errorf("HMAC() = %x, want %x", got, want)
			}
		})
	}
}
