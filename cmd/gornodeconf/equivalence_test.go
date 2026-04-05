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

func TestCompareCommandOutcomesReturnsNoDiffsForMatchingResults(t *testing.T) {
	t.Parallel()

	got := commandOutcome{
		stdout:   "hello\n",
		stderr:   "warn\n",
		exitCode: 0,
		files: map[string]string{
			"a.txt": "alpha",
			"b.txt": "bravo",
		},
	}
	want := got

	diffs := compareCommandOutcomes(got, want)
	if len(diffs) != 0 {
		t.Fatalf("expected no diffs, got %#v", diffs)
	}
}

func TestCompareCommandOutcomesReportsStableDiffs(t *testing.T) {
	t.Parallel()

	got := commandOutcome{
		stdout:   "hello\n",
		stderr:   "warn\n",
		exitCode: 1,
		files: map[string]string{
			"b.txt": "bravo",
			"c.txt": "charlie",
		},
	}
	want := commandOutcome{
		stdout:   "hola\n",
		stderr:   "warned\n",
		exitCode: 2,
		files: map[string]string{
			"a.txt": "alpha",
			"b.txt": "bravo-two",
		},
	}

	diffs := compareCommandOutcomes(got, want)
	gotDiffs := []outcomeDifference{
		{field: "stdout", got: "hello\n", want: "hola\n"},
		{field: "stderr", got: "warn\n", want: "warned\n"},
		{field: "exit code", got: "1", want: "2"},
		{field: "file", path: "a.txt", got: "<missing>", want: "alpha"},
		{field: "file", path: "b.txt", got: "bravo", want: "bravo-two"},
		{field: "file", path: "c.txt", got: "charlie", want: "<missing>"},
	}
	if !reflect.DeepEqual(diffs, gotDiffs) {
		t.Fatalf("diff mismatch:\n got: %#v\nwant: %#v", diffs, gotDiffs)
	}
}
