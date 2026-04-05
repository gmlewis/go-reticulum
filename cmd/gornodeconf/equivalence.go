// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

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

// compareCommandOutcomes returns a stable list of mismatches between two
// command outcomes. The caller can use this to record divergences between the
// Go and Python command traces.
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
