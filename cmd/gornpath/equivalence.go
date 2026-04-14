// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

import "sort"

// commandOutcome captures the observable results from a single command run.
type commandOutcome struct {
	stdout   string
	stderr   string
	exitCode int
	files    map[string]string
}

// outcomeDifference describes one mismatch between two command outcomes.
type outcomeDifference struct {
	field string
	path  string
	got   string
	want  string
}

// commandExecutor produces one observable command outcome.
type commandExecutor func() commandOutcome

// commandComparison bundles the results of two command executions together
// with the computed differences between them.
type commandComparison struct {
	left  commandOutcome
	right commandOutcome
	diffs []outcomeDifference
}

// runCommandComparison executes two command runners and compares the results.
func runCommandComparison(left, right commandExecutor) commandComparison {
	leftOutcome := left()
	rightOutcome := right()
	return commandComparison{
		left:  leftOutcome,
		right: rightOutcome,
		diffs: compareCommandOutcomes(leftOutcome, rightOutcome),
	}
}

// formatOutcomeDifferences renders comparison differences as stable,
// human-readable lines.
func formatOutcomeDifferences(diffs []outcomeDifference) []string {
	lines := make([]string, 0, len(diffs))
	for _, diff := range diffs {
		switch diff.field {
		case "file":
			lines = append(lines, "file["+diff.path+"] got="+quote(diff.got)+" want="+quote(diff.want))
		default:
			lines = append(lines, diff.field+" got="+quote(diff.got)+" want="+quote(diff.want))
		}
	}
	return lines
}

// compareCommandOutcomes returns a stable list of mismatches between two
// command outcomes.
func compareCommandOutcomes(got, want commandOutcome) []outcomeDifference {
	var diffs []outcomeDifference

	if got.stdout != want.stdout {
		diffs = append(diffs, outcomeDifference{field: "stdout", got: got.stdout, want: want.stdout})
	}
	if got.stderr != want.stderr {
		diffs = append(diffs, outcomeDifference{field: "stderr", got: got.stderr, want: want.stderr})
	}
	if got.exitCode != want.exitCode {
		diffs = append(diffs, outcomeDifference{field: "exit code", got: itoa(got.exitCode), want: itoa(want.exitCode)})
	}

	paths := make(map[string]struct{}, len(got.files)+len(want.files))
	for path := range got.files {
		paths[path] = struct{}{}
	}
	for path := range want.files {
		paths[path] = struct{}{}
	}

	sortedPaths := make([]string, 0, len(paths))
	for path := range paths {
		sortedPaths = append(sortedPaths, path)
	}
	sort.Strings(sortedPaths)

	for _, path := range sortedPaths {
		gotData, gotOK := got.files[path]
		wantData, wantOK := want.files[path]
		switch {
		case gotOK && !wantOK:
			diffs = append(diffs, outcomeDifference{field: "file", path: path, got: gotData, want: "<missing>"})
		case !gotOK && wantOK:
			diffs = append(diffs, outcomeDifference{field: "file", path: path, got: "<missing>", want: wantData})
		case gotData != wantData:
			diffs = append(diffs, outcomeDifference{field: "file", path: path, got: gotData, want: wantData})
		}
	}

	return diffs
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}

	var digits [20]byte
	index := len(digits)
	negative := value < 0
	if negative {
		value = -value
	}
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		index--
		digits[index] = '-'
	}
	return string(digits[index:])
}

func quote(value string) string {
	return "\"" + value + "\""
}
