// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux || darwin

package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecodePTYTCFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     any
		want    *ptyTCFlags
		wantErr string
	}{
		{
			name: "nil payload",
			raw:  nil,
			want: nil,
		},
		{
			name:    "invalid payload",
			raw:     "bad",
			wantErr: "invalid tcflags payload",
		},
		{
			name: "decodes fields",
			raw:  []any{1, uint64(2), float64(3), int32(4), 5, 6, []any{7, uint8(8)}},
			want: &ptyTCFlags{
				IFlag:  1,
				OFlag:  2,
				CFlag:  3,
				LFlag:  4,
				ISpeed: 5,
				OSpeed: 6,
				CC:     []uint8{7, 8},
			},
		},
		{
			name:    "invalid control chars",
			raw:     []any{1, 2, 3, 4, 5, 6, []any{"bad"}},
			wantErr: "invalid control character value",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := decodePTYTCFlags(tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("decodePTYTCFlags() error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("decodePTYTCFlags() error = %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("decodePTYTCFlags() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
