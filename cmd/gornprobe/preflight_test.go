// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSplitProbeFullName(t *testing.T) {
	t.Parallel()

	appName, aspects := splitProbeFullName("gornprobe.debug.alpha")
	if appName != "gornprobe" {
		t.Fatalf("app name = %q, want %q", appName, "gornprobe")
	}
	if got, want := aspects, []string{"debug", "alpha"}; !bytes.Equal([]byte(strings.Join(got, ",")), []byte(strings.Join(want, ","))) {
		t.Fatalf("aspects = %v, want %v", got, want)
	}
}

func TestParseProbeDestinationHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    []byte
		wantErr string
	}{
		{
			name:  "valid",
			input: "00112233445566778899aabbccddeeff",
			want:  []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		},
		{
			name:    "wrong length",
			input:   "001122",
			wantErr: "Destination length is invalid, must be 32 hexadecimal characters (16 bytes).",
		},
		{
			name:    "invalid hex",
			input:   strings.Repeat("z", 32),
			wantErr: "Invalid destination entered. Check your input.",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseProbeDestinationHash(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("error = %q, want %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseProbeDestinationHash failed: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("hash = %x, want %x", got, tc.want)
			}
		})
	}
}

func TestProbeTimeoutSeconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		timeout  float64
		firstHop int
		want     float64
	}{
		{name: "fallback adds first hop", timeout: 0, firstHop: 7, want: DefaultTimeout + 7},
		{name: "explicit timeout wins", timeout: 3.5, firstHop: 7, want: 3.5},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := probeTimeoutSeconds(tc.timeout, tc.firstHop); got != tc.want {
				t.Fatalf("probeTimeoutSeconds(%v, %v) = %v, want %v", tc.timeout, tc.firstHop, got, tc.want)
			}
		})
	}
}
