// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
	"time"
)

func TestPrettyDateFormatsBoundaryValues(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name string
		then time.Time
		want string
	}{
		{name: "0_seconds", then: now, want: "0 seconds"},
		{name: "seconds", then: now.Add(-9 * time.Second), want: "9 seconds"},
		{name: "minute", then: now.Add(-60 * time.Second), want: "1 minute"},
		{name: "minutes", then: now.Add(-3599 * time.Second), want: "59 minutes"},
		{name: "an_hour", then: now.Add(-3600 * time.Second), want: "an hour"},
		{name: "hours", then: now.Add(-7200 * time.Second), want: "2 hours"},
		{name: "1_day", then: now.Add(-24 * time.Hour), want: "1 day"},
		{name: "2_days", then: now.Add(-2 * 24 * time.Hour), want: "2 days"},
		{name: "6_days", then: now.Add(-6 * 24 * time.Hour), want: "6 days"},
		{name: "2_weeks", then: now.Add(-14 * 24 * time.Hour), want: "2 weeks"},
		{name: "2_months", then: now.Add(-60 * 24 * time.Hour), want: "2 months"},
		{name: "1_years", then: now.Add(-365 * 24 * time.Hour), want: "1 years"},
		{name: "future", then: now.Add(1 * time.Second), want: ""},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := prettyDateAt(now, test.then); got != test.want {
				t.Fatalf("prettyDateAt mismatch: got %q want %q", got, test.want)
			}
		})
	}
}
