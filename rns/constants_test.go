// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "testing"

func TestPrettySize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input  float64
		suffix string
		want   string
	}{
		{0, "B", "0 B"},
		{1, "B", "1 B"},
		{999, "B", "999 B"},
		{1000, "B", "1.00 KB"},
		{1500, "B", "1.50 KB"},
		{1e6, "B", "1.00 MB"},
		{1.5e6, "B", "1.50 MB"},
		{1e9, "B", "1.00 GB"},
		{1e12, "B", "1.00 TB"},
		{1e15, "B", "1.00 PB"},
		{1e18, "B", "1.00 EB"},
		{1e21, "B", "1.00 ZB"},
		{1e24, "B", "1.00YB"},
		{1e27, "B", "1000.00YB"},
		{0, "b", "0 b"},
		{1, "b", "8 b"},
		{1000, "b", "8.00 Kb"},
		{1e6, "b", "8.00 Mb"},
	}
	for _, tc := range tests {
		name := tc.want
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := PrettySize(tc.input, tc.suffix)
			if got != tc.want {
				t.Errorf("PrettySize(%v, %q) = %q, want %q", tc.input, tc.suffix, got, tc.want)
			}
		})
	}
}

func TestPrettySpeed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0 bps"},
		{8, "8 bps"},
		{8000, "8.00 Kbps"},
		{8e6, "8.00 Mbps"},
		{8e9, "8.00 Gbps"},
		{8e24, "8.00Ybps"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := PrettySpeed(tc.input)
			if got != tc.want {
				t.Errorf("PrettySpeed(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestPrettyFrequency(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input float64
		want  string
	}{
		{0, "0.00 µHz"},
		{0.00001, "10.00 µHz"},
		{0.0001, "100.00 µHz"},
		{0.001, "1.00 mHz"},
		{0.01, "10.00 mHz"},
		{0.1, "100.00 mHz"},
		{1.0, "1.00 Hz"},
		{10.0, "10.00 Hz"},
		{100.0, "100.00 Hz"},
		{1000.0, "1.00 KHz"},
		{1000000.0, "1.00 MHz"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := PrettyFrequency(tc.input)
			if got != tc.want {
				t.Errorf("PrettyFrequency(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
