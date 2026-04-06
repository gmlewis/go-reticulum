// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
)

func TestHasHelp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "none", args: nil, want: false},
		{name: "short", args: []string{"-h"}, want: true},
		{name: "long", args: []string{"--help"}, want: true},
		{name: "other flag", args: []string{"--version"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasHelp(tc.args); got != tc.want {
				t.Fatalf("hasHelp(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseArgs(t *testing.T) {
	t.Parallel()
	opts, port, err := parseArgs([]string{"--sign", "--firmware-hash", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff", "ttyUSB0"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !opts.sign || opts.firmwareHash != "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff" || port != "ttyUSB0" {
		t.Fatalf("unexpected parse state: opts=%+v port=%q", opts, port)
	}
}

func TestParseArgsVersion(t *testing.T) {
	t.Parallel()
	opts, port, err := parseArgs([]string{"--version"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !opts.version {
		t.Fatal("version = false, want true")
	}
	if port != "" {
		t.Fatalf("port = %q, want empty", port)
	}
}

func TestParseArgsNoPort(t *testing.T) {
	t.Parallel()
	opts, port, err := parseArgs([]string{"--version"})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if port != "" {
		t.Fatalf("port = %q, want empty", port)
	}
	if !opts.version || opts.debug {
		t.Fatalf("unexpected parser state: %+v", opts)
	}
}
