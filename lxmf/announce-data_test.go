// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package lxmf

import (
	"reflect"
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
		{
			name: "v0.5.0 msgpack str name fails closed",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{"Alice"})
				return data
			}(),
			want: "",
		},
		{
			name: "v0.5.0 msgpack invalid utf8 bytes fail closed",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{[]byte{0xff}})
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
		{
			name: "msgpack list with bool true stamp cost",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{[]byte("Carol"), true})
				return data
			}(),
			want:   1,
			wantOK: true,
		},
		{
			name: "msgpack list with bool false stamp cost",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{[]byte("Carol"), false})
				return data
			}(),
			want:   0,
			wantOK: true,
		},
		{
			name: "msgpack list with nil stamp cost",
			appData: func() []byte {
				data, _ := msgpack.Pack([]any{[]byte("Carol"), nil})
				return data
			}(),
			wantOK: false,
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

func TestStampCostFromAppDataOutcomePreservesRawNonCanonicalValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		stampCost any
	}{
		{
			name:      "float",
			stampCost: 1.5,
		},
		{
			name:      "bytes",
			stampCost: []byte{0x01, 0x02},
		},
		{
			name:      "string",
			stampCost: "abc",
		},
		{
			name:      "list",
			stampCost: []any{int64(1)},
		},
		{
			name:      "dict",
			stampCost: map[any]any{"a": int64(1)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			appData, err := msgpack.Pack([]any{[]byte("Carol"), tc.stampCost})
			if err != nil {
				t.Fatalf("Pack(): %v", err)
			}

			got, ok, clear, err := stampCostFromAppDataOutcome(appData)
			if err != nil {
				t.Fatalf("stampCostFromAppDataOutcome(): %v", err)
			}
			if !ok {
				t.Fatal("stampCostFromAppDataOutcome() did not preserve raw stamp cost")
			}
			if clear {
				t.Fatal("stampCostFromAppDataOutcome() requested cache clear for raw stamp cost")
			}
			if !reflect.DeepEqual(got, tc.stampCost) {
				t.Fatalf("stampCostFromAppDataOutcome()=%#v want %#v", got, tc.stampCost)
			}

			converted, convertedOK := StampCostFromAppData(appData)
			if convertedOK {
				t.Fatalf("StampCostFromAppData()=(%v,%v), want non-canonical raw value to stay non-int", converted, convertedOK)
			}
		})
	}
}
