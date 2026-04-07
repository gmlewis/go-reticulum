// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
)

func TestSizeStr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		num    float64
		suffix string
		want   string
	}{
		{100, "B", "100 B"},
		{1000, "B", "1.00 KB"},
		{1500, "B", "1.50 KB"},
		{1000000, "B", "1.00 MB"},
		{1000000000, "B", "1.00 GB"},
		{100, "b", "800 b"},
		{1000, "b", "8.00 Kb"},
		{125, "b", "1.00 Kb"},
		{0, "B", "0 B"},
		{999, "B", "999 B"},
		{1000000000000, "B", "1.00 TB"},
		{1000000000000000, "B", "1.00 PB"},
		{1000000000000000000, "B", "1.00 EB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := sizeStr(tt.num, tt.suffix)
			if got != tt.want {
				t.Errorf("sizeStr(%v, %q) = %q, want %q", tt.num, tt.suffix, got, tt.want)
			}
		})
	}
}
