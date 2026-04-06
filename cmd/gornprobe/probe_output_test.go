// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestProbeMTUOverflowMessage(t *testing.T) {
	t.Parallel()

	if got, want := formatProbeMTUError(513), "Error: Probe packet size of 513 bytes exceed MTU of 500 bytes"; got != want {
		t.Fatalf("formatProbeMTUError() = %q, want %q", got, want)
	}
}

func TestProbeSentLine(t *testing.T) {
	t.Parallel()

	if got, want := formatProbeSentLine(1, 16, []byte{0xaa, 0xbb}, ""), "\rSent probe 1 (16 bytes) to "+rns.PrettyHex([]byte{0xaa, 0xbb})+"  "; got != want {
		t.Fatalf("formatProbeSentLine() = %q, want %q", got, want)
	}
}

func TestProbeRTTString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   float64
		want string
	}{
		{name: "seconds", in: 1.23456, want: "1.235 seconds"},
		{name: "milliseconds", in: 0.1, want: "100.0 milliseconds"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := formatProbeRTTString(tc.in); got != tc.want {
				t.Fatalf("formatProbeRTTString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestProbeHopSuffix(t *testing.T) {
	t.Parallel()

	if got, want := formatProbeHopSuffix(1), ""; got != want {
		t.Fatalf("formatProbeHopSuffix(1) = %q, want %q", got, want)
	}
	if got, want := formatProbeHopSuffix(2), "s"; got != want {
		t.Fatalf("formatProbeHopSuffix(2) = %q, want %q", got, want)
	}
}

func TestProbeLossSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sent     int
		received int
		wantText string
		wantExit int
	}{
		{name: "no loss", sent: 4, received: 4, wantText: "Sent 4, received 4, packet loss 0.00%", wantExit: 0},
		{name: "partial loss", sent: 10, received: 7, wantText: "Sent 10, received 7, packet loss 30.00%", wantExit: 2},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotText, gotExit := formatProbeLossSummary(tc.sent, tc.received)
			if gotText != tc.wantText {
				t.Fatalf("formatProbeLossSummary text = %q, want %q", gotText, tc.wantText)
			}
			if gotExit != tc.wantExit {
				t.Fatalf("formatProbeLossSummary exit = %v, want %v", gotExit, tc.wantExit)
			}
		})
	}
}
