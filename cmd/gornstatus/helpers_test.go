// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
)

func TestSpeedStr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0.00 bps"},
		{1, "1.00 bps"},
		{999, "999.00 bps"},
		{1000, "1.00 kbps"},
		{1500, "1.50 kbps"},
		{999999, "1000.00 kbps"},
		{1000000, "1.00 Mbps"},
		{1500000000, "1.50 Gbps"},
		{999999999999999, "1000.00 Tbps"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := speedStr(tc.input)
			if got != tc.want {
				t.Errorf("speedStr(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSizeStr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{999, "999 B"},
		{1000, "1.00 KB"},
		{1500, "1.50 KB"},
		{999999, "1000.00 KB"},
		{1000000, "1.00 MB"},
		{1500000000, "1.50 GB"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := rns.PrettySize(tc.input, "B")
			if got != tc.want {
				t.Errorf("PrettySize(%v, \"B\") = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
