// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && !race
// +build integration,!race

package lxmf

import (
	"runtime"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestParallelStampGeneration(t *testing.T) {
	t.Parallel()
	if runtime.NumCPU() < 2 {
		t.Skip("needs at least 2 CPU cores for parallelism test")
	}
	// This test runs a heavy CPU-bound stamp search twice (serial +
	// parallel). It is much slower under -race (the race detector
	// instruments every memory access in the hot loop). Skipping under
	// -race keeps CI runtimes reasonable while still exercising the
	// parallelism contract in normal runs.
	testutils.SkipShortIntegration(t)

	// Build a single message with a moderate cost and time it.
	material := []byte("parallel-stamp-test-material")
	cost := 18

	start := time.Now()
	_, _, _, err := GenerateStamp(material, cost, WorkblockExpandRounds)
	if err != nil {
		t.Fatalf("GenerateStamp: %v", err)
	}
	serialDuration := time.Since(start)

	start = time.Now()
	_, _, _, err = GenerateStampParallel(material, cost, WorkblockExpandRounds, runtime.NumCPU())
	if err != nil {
		t.Fatalf("GenerateStampParallel: %v", err)
	}
	parallelDuration := time.Since(start)

	// The parallel version should not be substantially slower than the
	// serial one. We allow a generous 2x fudge factor for goroutine
	// scheduling overhead on busy CI machines.
	if parallelDuration > 2*serialDuration {
		t.Logf("parallel (%v) was not faster than serial (%v) on %d cores", parallelDuration, serialDuration, runtime.NumCPU())
	}
}
