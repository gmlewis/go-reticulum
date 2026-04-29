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
	t.Parallel()
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

func TestDisplayNameFromAppDataPanicsOnMalformedEncodings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		appData []byte
	}{
		{
			name:    "raw invalid utf8",
			appData: []byte{0xff},
		},
		{
			name:    "malformed msgpack array",
			appData: []byte{0x91, 0xc1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("DisplayNameFromAppData(%v) did not panic", tc.appData)
				}
			}()
			_ = DisplayNameFromAppData(tc.appData)
		})
	}
}

func TestStampCostFromAppData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		appData []byte
		want    int
		wantOK  bool
	}{
		{
			name:   "nil data",
			wantOK: false,
		},
		{
			name:    "raw utf8",
			appData: []byte("Alice"),
			wantOK:  false,
		},
		{
			name: "msgpack list without stamp cost",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{[]byte("Bob")})
				return data
			}(),
			wantOK: false,
		},
		{
			name: "msgpack list with stamp cost",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{[]byte("Carol"), 8})
				return data
			}(),
			want:   8,
			wantOK: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := StampCostFromAppData(tc.appData)
			if ok != tc.wantOK {
				t.Fatalf("StampCostFromAppData(%v) ok=%v want=%v", tc.appData, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("StampCostFromAppData(%v) = %v, want %v", tc.appData, got, tc.want)
			}
		})
	}
}

func TestStampCostFromAppDataPanicsOnMalformedMsgpack(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("StampCostFromAppData() did not panic")
		}
	}()

	_, _ = StampCostFromAppData([]byte{0x91, 0xc1})
}
