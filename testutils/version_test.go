// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"regexp"
	"strings"
	"testing"
)

func TestRNSVersionFromSourceReadsTheVersionFile(t *testing.T) {
	t.Parallel()

	got := RNSVersionFromSource(t)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(got) {
		t.Errorf("RNSVersionFromSource() = %q, want a dotted version from rns/version.go", got)
	}
}

// TestVersionFlagRetriesWhenTheArtifactIsNewerThanTheSource covers the version
// bump this helper exists for: the first probe sees an artifact whose reported
// version no longer matches the source, and the retry succeeds once the probe
// rebuilds from the bumped source.
func TestVersionFlagRetriesWhenTheArtifactIsNewerThanTheSource(t *testing.T) {
	t.Parallel()

	version := RNSVersionFromSource(t)
	calls := 0
	got := VersionFlag(t, "gornir", func(*testing.T) string {
		calls++
		if calls == 1 {
			return "gornir 0.0.0-stale\n"
		}
		return "gornir " + version + "\n"
	})

	if calls != 2 {
		t.Errorf("probe calls = %v, want 2 (one stale attempt, one rebuild)", calls)
	}
	if want := "gornir " + version + "\n"; got != want {
		t.Errorf("VersionFlag() = %q, want the output of the agreeing probe %q", got, want)
	}
}

// TestVersionFlagRejectsAWrongVersion keeps the assertion honest: an artifact
// that never reports the source version, or prefixes it with another program
// name, must be reported as a mismatch instead of passing.
func TestVersionFlagRejectsAWrongVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		prog  string
		probe func() string
	}{
		{
			name:  "wrong version",
			prog:  "gornir",
			probe: func() string { return "gornir 0.0.0-wrong\n" },
		},
		{
			name:  "wrong program name",
			prog:  "gornir",
			probe: func() string { return "not-gornir 9.9.9\n" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, want, _, ok := versionFlag(tt.prog, tt.probe, func() string { return "9.9.9" })
			if ok {
				t.Fatalf("versionFlag accepted %q as %q", got, want)
			}
			if got != strings.TrimSpace(tt.probe()) || want != tt.prog+" 9.9.9" {
				t.Errorf("versionFlag mismatch = (%q, %q), want the observed and expected outputs", got, want)
			}
		})
	}
}
