// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import (
	"reflect"
	"testing"
)

func TestRunEquivalenceScenarioAggregatesStepComparisons(t *testing.T) {
	t.Parallel()

	leftCalls := 0
	rightCalls := 0
	scenario := equivalenceScenario{
		fixture: defaultEquivalenceFixtures()[0],
		steps: []equivalenceStep{
			{
				name: "bootstrap",
				left: func() commandOutcome {
					leftCalls++
					return commandOutcome{stdout: "ok", exitCode: 0}
				},
				right: func() commandOutcome {
					rightCalls++
					return commandOutcome{stdout: "ok", exitCode: 0}
				},
			},
			{
				name: "flash",
				left: func() commandOutcome {
					leftCalls++
					return commandOutcome{stdout: "left", exitCode: 1, files: map[string]string{"left.txt": "alpha"}}
				},
				right: func() commandOutcome {
					rightCalls++
					return commandOutcome{stdout: "right", exitCode: 2, files: map[string]string{"right.txt": "bravo"}}
				},
			},
		},
	}

	report := runEquivalenceScenario(scenario)
	if leftCalls != 2 || rightCalls != 2 {
		t.Fatalf("expected both sides to run twice, got left=%v right=%v", leftCalls, rightCalls)
	}
	if report.fixture != scenario.fixture {
		t.Fatalf("fixture mismatch: got %#v want %#v", report.fixture, scenario.fixture)
	}
	if len(report.steps) != 2 {
		t.Fatalf("step count mismatch: got %v want 2", len(report.steps))
	}
	if report.steps[0].name != "bootstrap" || report.steps[1].name != "flash" {
		t.Fatalf("step names mismatch: %#v", report.steps)
	}
	if len(report.steps[0].comparison.diffs) != 0 {
		t.Fatalf("expected first step to match, got %#v", report.steps[0].comparison.diffs)
	}
	if len(report.diffs) != 4 {
		t.Fatalf("diff count mismatch: got %v want 4", len(report.diffs))
	}
	want := []outcomeDifference{
		{field: "stdout", got: "left", want: "right"},
		{field: "exit code", got: "1", want: "2"},
		{field: "file", path: "left.txt", got: "alpha", want: "<missing>"},
		{field: "file", path: "right.txt", got: "<missing>", want: "bravo"},
	}
	if !reflect.DeepEqual(report.diffs, want) {
		t.Fatalf("report diffs mismatch:\n got: %#v\nwant: %#v", report.diffs, want)
	}
}
