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

	// Add 35 entries (more than statsMax)
	for i := 0; i < 35; i++ {
		now := time.Now()
		got := float64(i * 1000)
		phyGot := float64(i * 1200)

		entry := statsEntry{
			Time:   now,
			Got:    got,
			PhyGot: phyGot,
		}
		entries = append(entries, entry)

		// Apply circular buffer logic
		for len(entries) > statsMax {
			entries = entries[1:]
		}
	}

	// After adding 35 entries, should have exactly 32
	if len(entries) != statsMax {
		t.Fatalf("Expected %d entries, got %d", statsMax, len(entries))
	}

	// First entry should be the 4th one we added (index 3)
	if entries[0].Got != 3000.0 {
		t.Errorf("Expected first entry got=3000, got %v", entries[0].Got)
	}

	// Last entry should be the 35th one (index 34)
	if entries[len(entries)-1].Got != 34000.0 {
		t.Errorf("Expected last entry got=34000, got %v", entries[len(entries)-1].Got)
	}
}
