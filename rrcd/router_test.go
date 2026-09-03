// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rrcd/cbor"
)

// capsBody builds a HELLO body map from alternating key/value arguments.
func capsBody(keyVals ...any) *cbor.Map {
	m := cbor.NewMap()
	for i := 0; i+1 < len(keyVals); i += 2 {
		m.Set(keyVals[i], keyVals[i+1])
	}
	return m
}

func TestRouterNormRoom(t *testing.T) {
	t.Parallel()

	r := NewRouter(RouterHooks{
		MaxRoomNameBytes: func() int { return 64 },
	})

	tests := []struct {
		room    string
		want    string
		wantErr string
	}{
		{room: "Lobby", want: "lobby"},
		{room: "  MiXeD CaSe  ", want: "mixed case"},
		{room: "\t\n Lobby \r\n", want: "lobby"},
		// Python str.strip() removes NBSP and the Unicode line
		// separator in addition to ASCII whitespace.
		{room: "\u00a0Lobby\u2028", want: "lobby"},
		{room: "#Foo Bar", want: "#foo bar"},
		{room: strings.Repeat("a", 64), want: strings.Repeat("a", 64)},
		{room: strings.Repeat("a", 65),
			wantErr: "room name too long: 65 bytes > 64 bytes"},
		// The limit counts UTF-8 bytes, not runes: 32 two-byte runes
		// equal 64 bytes and pass, 33 equal 66 bytes and fail.
		{room: strings.Repeat("\u00e9", 32), want: strings.Repeat("\u00e9", 32)},
		{room: strings.Repeat("\u00e9", 33),
			wantErr: "room name too long: 66 bytes > 64 bytes"},
		{room: "", wantErr: "room name must not be empty"},
		{room: "   \t\v\f ", wantErr: "room name must not be empty"},
	}

	for _, tt := range tests {
		got, err := r.normRoom(tt.room)
		if tt.wantErr == "" {
			if err != nil {
				t.Errorf("normRoom(%q) unexpected error: %v", tt.room, err)
				continue
			}
			if got != tt.want {
				t.Errorf("normRoom(%q):\n got %q\nwant %q", tt.room, got, tt.want)
			}
			continue
		}
		if err == nil {
			t.Errorf("normRoom(%q) expected error %q, got nil", tt.room, tt.wantErr)
			continue
		}
		if err.Error() != tt.wantErr {
			t.Errorf("normRoom(%q) error:\n got %q\nwant %q", tt.room, err.Error(), tt.wantErr)
		}
	}
}

func TestRouterExtractCaps(t *testing.T) {
	t.Parallel()

	r := NewRouter(RouterHooks{})

	tests := []struct {
		name string
		body *cbor.Map
		want map[int64]any
	}{
		{
			name: "nil body",
			body: nil,
			want: map[int64]any{},
		},
		{
			name: "no caps key",
			body: cbor.NewMap(),
			want: map[int64]any{},
		},
		{
			name: "caps not a map (int)",
			body: capsBody(BHelloCaps, int64(7)),
			want: map[int64]any{},
		},
		{
			name: "caps not a map (string)",
			body: capsBody(BHelloCaps, "caps"),
			want: map[int64]any{},
		},
		{
			name: "caps key present but nil",
			body: capsBody(BHelloCaps, nil),
			want: map[int64]any{},
		},
		{
			name: "int-like keys kept, bools collapse to 1/0",
			body: capsBody(BHelloCaps,
				capsBody(int64(1), true, int64(5), "text",
					uint64(3), int64(7), false, "off")),
			want: map[int64]any{
				1: true,
				5: "text",
				3: int64(7),
				0: "off",
			},
		},
		{
			name: "non-int-like keys dropped",
			body: capsBody(BHelloCaps,
				capsBody("text", "v", 2.5, "w", int64(9), "kept")),
			want: map[int64]any{9: "kept"},
		},
		{
			name: "other body keys ignored",
			body: capsBody(int64(9), "noise", BHelloCaps, capsBody(int64(4), true)),
			want: map[int64]any{4: true},
		},
	}

	for _, tt := range tests {
		got := r.extractCaps(tt.body)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%v: extractCaps():\n got %#v\nwant %#v", tt.name, got, tt.want)
		}
	}
}
