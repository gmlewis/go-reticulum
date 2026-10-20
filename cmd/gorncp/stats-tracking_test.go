// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
	"time"
)

func TestStatsTrackingCircularBuffer(t *testing.T) {
	t.Parallel()

	statsMax := 32
	entries := make([]statsEntry, 0)

	// Add 35 entries (more than statsMax).
	for i := range 35 {
		now := time.Now()
		got := float64(i * 1000)
		phyGot := float64(i * 1200)

		entry := statsEntry{
			Time:   now,
			Got:    got,
			PhyGot: phyGot,
		}
		entries = append(entries, entry)

		for len(entries) > statsMax {
			entries = entries[1:]
		}
	}

	if len(entries) != statsMax {
		t.Fatalf("Expected %v entries, got %v", statsMax, len(entries))
	}

	// The oldest three entries have been dropped.
	if entries[0].Got != 3000.0 {
		t.Errorf("Expected first entry got=3000, got %v", entries[0].Got)
	}

	if entries[len(entries)-1].Got != 34000.0 {
		t.Errorf("Expected last entry got=34000, got %v", entries[len(entries)-1].Got)
	}
}
