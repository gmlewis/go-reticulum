// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "testing"

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
