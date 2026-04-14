// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

// equivalenceStep captures one side-by-side command pair.
type equivalenceStep struct {
	name  string
	left  commandExecutor
	right commandExecutor
}

// equivalenceStepComparison records the results of one step.
type equivalenceStepComparison struct {
	name       string
	comparison commandComparison
}

// equivalenceScenario bundles one fixture with the commands that should be run
// against both the Python and Go implementations.
type equivalenceScenario struct {
	fixture equivalenceFixture
	steps   []equivalenceStep
}

// equivalenceScenarioReport captures the full ordered comparison result for a
// single fixture.
type equivalenceScenarioReport struct {
	fixture equivalenceFixture
	steps   []equivalenceStepComparison
	diffs   []outcomeDifference
}

// runEquivalenceScenario executes each step in order and returns the combined
// comparison report.
func runEquivalenceScenario(scenario equivalenceScenario) equivalenceScenarioReport {
	report := equivalenceScenarioReport{fixture: scenario.fixture}
	for _, step := range scenario.steps {
		comparison := runCommandComparison(step.left, step.right)
		report.steps = append(report.steps, equivalenceStepComparison{name: step.name, comparison: comparison})
		report.diffs = append(report.diffs, comparison.diffs...)
	}
	return report
}
