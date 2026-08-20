// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration
// +build integration

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestRunEquivalenceScenarioAggregatesStepComparisons(t *testing.T) {
	t.Parallel()

	leftCalls := 0
	rightCalls := 0
	scenario := equivalenceScenario{
		fixture: equivalenceFixture{name: "aggregates"},
		steps: []equivalenceStep{
			{
				name: "version",
				left: func() commandOutcome {
					leftCalls++
					return commandOutcome{stdout: "gornpath 0.1.0\n", exitCode: 0}
				},
				right: func() commandOutcome {
					rightCalls++
					return commandOutcome{stdout: "rnpath 0.1.0\n", exitCode: 0}
				},
			},
			{
				name: "help",
				left: func() commandOutcome {
					leftCalls++
					return commandOutcome{stdout: "usage: gornpath\n", exitCode: 0, files: map[string]string{"left.txt": "alpha"}}
				},
				right: func() commandOutcome {
					rightCalls++
					return commandOutcome{stdout: "usage: rnpath\n", exitCode: 0, files: map[string]string{"right.txt": "bravo"}}
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
	if report.steps[0].name != "version" || report.steps[1].name != "help" {
		t.Fatalf("step names mismatch: %#v", report.steps)
	}
	if len(report.steps[0].comparison.diffs) != 1 {
		t.Fatalf("expected first step to differ, got %#v", report.steps[0].comparison.diffs)
	}
	if len(report.diffs) != 4 {
		t.Fatalf("diff count mismatch: got %v want 4", len(report.diffs))
	}
	want := []outcomeDifference{
		{field: "stdout", got: "gornpath 0.1.0\n", want: "rnpath 0.1.0\n"},
		{field: "stdout", got: "usage: gornpath\n", want: "usage: rnpath\n"},
		{field: "file", path: "left.txt", got: "alpha", want: "<missing>"},
		{field: "file", path: "right.txt", got: "<missing>", want: "bravo"},
	}
	if !reflect.DeepEqual(report.diffs, want) {
		t.Fatalf("report diffs mismatch:\n got: %#v\nwant: %#v", report.diffs, want)
	}
}

// TestRunEquivalenceScenarioComparesRealVersionCommands is a LIVE cross-impl
// test: it runs the real `gornpath --version` (Go) and `rnpath.py --version`
// (Python) and confirms the equivalence harness surfaces their (expected)
// stdout difference. The two binaries intentionally print different program
// names, so a non-empty diff is the live signal that both ran and were diffed.
func TestRunEquivalenceScenarioComparesRealVersionCommands(t *testing.T) {
	t.Parallel()
	testutils.SkipShortIntegration(t)

	fixture := equivalenceFixture{name: "version"}
	scenario := equivalenceScenario{
		fixture: fixture,
		steps: []equivalenceStep{{
			name:  "version",
			left:  func() commandOutcome { return runGoGornpathOutcome(t, "--version") },
			right: func() commandOutcome { return runPythonRnpathOutcome(t, "--version") },
		}},
	}

	report := runEquivalenceScenario(scenario)
	if len(report.steps) != 1 {
		t.Fatalf("unexpected step count: %v", len(report.steps))
	}
	if report.steps[0].name != "version" {
		t.Fatalf("unexpected step name: %q", report.steps[0].name)
	}
	if report.steps[0].comparison.left.exitCode != 0 || report.steps[0].comparison.right.exitCode != 0 {
		t.Fatalf("expected both commands to succeed: %#v", report.steps[0].comparison)
	}
	if len(report.diffs) == 0 {
		t.Fatal("expected version commands to differ in output")
	}
	if report.diffs[0].field != "stdout" {
		t.Fatalf("expected stdout diff first, got %#v", report.diffs[0])
	}
}

func runGoGornpathOutcome(t *testing.T, args ...string) commandOutcome {
	t.Helper()

	cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
	cmd.Dir = "."
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return commandOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: commandExitCode(err)}
}

func runPythonRnpathOutcome(t *testing.T, args ...string) commandOutcome {
	t.Helper()

	repoDir := os.Getenv("ORIGINAL_RETICULUM_REPO_DIR")
	if repoDir == "" {
		t.Fatal("missing required environment variable ORIGINAL_RETICULUM_REPO_DIR (set by scripts/test-integration.sh)")
	}
	scriptPath := filepath.Join(repoDir, "RNS", "Utilities", "rnpath.py")
	cmd := exec.Command("python3", append([]string{"-u", scriptPath}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+repoDir)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return commandOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: commandExitCode(err)}
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}
