// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns/msgpack"
)

func TestDisplayNameFromAppData(t *testing.T) {
	tests := []struct {
		name    string
		appData []byte
		want    string
	}{
		{
			name:    "nil data",
			appData: nil,
			want:    "",
		},
		{
			name:    "empty data",
			appData: []byte{},
			want:    "",
		},
		{
			name:    "original format raw UTF-8",
			appData: []byte("Alice"),
			want:    "Alice",
		},
		{
			name: "v0.5.0 msgpack list format",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{[]byte("Bob")})
				return data
			}(),
			want: "Bob",
		},
		{
			name: "v0.5.0 msgpack list with stamp cost",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{[]byte("Carol"), 8})
				return data
			}(),
			want: "Carol",
		},
		{
			name: "v0.5.0 msgpack empty list",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{})
				return data
			}(),
			want: "",
		},
		{
			name: "v0.5.0 msgpack nil name",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{nil})
				return data
			}(),
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DisplayNameFromAppData(tc.appData)
			if got != tc.want {
				t.Fatalf("DisplayNameFromAppData(%v) = %q, want %q", tc.appData, got, tc.want)
			}
		})
	}
}
