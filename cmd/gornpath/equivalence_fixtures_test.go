// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build linux

package main

import "testing"

func TestDefaultEquivalenceFixturesCoverRepresentativeScenarios(t *testing.T) {
	t.Parallel()

	fixtures := defaultEquivalenceFixtures()
	if len(fixtures) != 6 {
		t.Fatalf("fixture count mismatch: got %v want 6", len(fixtures))
	}

	want := map[string]equivalenceFixture{
		"local-table": {name: "local-table"},
		"discovery":   {name: "discovery"},
		"drop":        {name: "drop"},
		"rates":       {name: "rates"},
		"blackhole":   {name: "blackhole"},
		"remote-link": {name: "remote-link"},
	}

	for _, fixture := range fixtures {
		if got, ok := want[fixture.name]; !ok {
			t.Fatalf("unexpected fixture %q", fixture.name)
		} else if got != fixture {
			t.Fatalf("fixture %q mismatch:\n got: %#v\nwant: %#v", fixture.name, fixture, got)
		}
		delete(want, fixture.name)
	}
	if len(want) != 0 {
		t.Fatalf("missing fixtures: %#v", want)
	}
}
