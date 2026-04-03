// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
	"time"
)

func TestTeardownSleepDurations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		call     func(func(time.Duration))
		wantWait time.Duration
	}{
		{
			name:     "fetch completion",
			call:     sleepAfterFetchCompletion,
			wantWait: 100 * time.Millisecond,
		},
		{
			name:     "fetch failure",
			call:     sleepAfterFetchFailure,
			wantWait: 150 * time.Millisecond,
		},
		{
			name:     "send completion",
			call:     sleepAfterSendCompletion,
			wantWait: 250 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []time.Duration
			recorder := func(wait time.Duration) {
				got = append(got, wait)
			}

			tt.call(recorder)

			if len(got) != 1 {
				t.Fatalf("sleep calls = %d, want 1", len(got))
			}
			if got[0] != tt.wantWait {
				t.Fatalf("sleep duration = %v, want %v", got[0], tt.wantWait)
			}
		})
	}
}
