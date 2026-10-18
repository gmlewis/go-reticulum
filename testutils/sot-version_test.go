// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestAssertSOTRNSVersionAcceptsOnlyVerifiedReleases pins the version pin
// itself: the releases the vector suites were verified against pass, and
// anything else fails. A pin that accepted everything would leave a vector run
// against an unmeasured release looking exactly like a passing parity check.
func TestAssertSOTRNSVersionAcceptsOnlyVerifiedReleases(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		version    string
		wantReject bool
	}{
		{"1.4.2", false},
		{"1.5.0", false},
		{"1.6.0", true},
		{"1.3.5", true},
		{"1.5", true},
		{"", true},
	} {
		t.Run(tc.version, func(t *testing.T) {
			t.Parallel()
			fake := &fakeTB{}
			AssertSOTRNSVersion(fake, "python3", tc.version)
			if got := len(fake.errors) > 0; got != tc.wantReject {
				t.Errorf("AssertSOTRNSVersion(%q) rejected = %v, want %v (errors: %v)",
					tc.version, got, tc.wantReject, fake.errors)
			}
		})
	}
}

// TestAssertSOTRNSVersionDriftHatchDowngradesToAWarning pins the deliberate
// escape hatch: with GORETICULUM_ALLOW_RNS_DRIFT set, comparing against an
// unverified release is a logged warning rather than a failure, so a developer
// can measure another release on purpose without editing the pin. Not parallel:
// it sets an environment variable.
func TestAssertSOTRNSVersionDriftHatchDowngradesToAWarning(t *testing.T) {
	t.Setenv(allowRNSDriftEnv, "1")

	fake := &fakeTB{}
	AssertSOTRNSVersion(fake, "python3", "9.9.9")
	if len(fake.errors) != 0 {
		t.Errorf("with %v set, an unverified release still failed: %v", allowRNSDriftEnv, fake.errors)
	}
	if len(fake.logs) == 0 {
		t.Errorf("with %v set, the mismatch was not reported as a warning at all", allowRNSDriftEnv)
	}
}

// TestSOTRNSVersionsCoversTheVersionCIPins keeps the pin and CI in step: the
// release `.github/workflows/build.yml` installs is the release every
// Python-derived vector suite runs against in CI, so it has to be one of the
// versions the vectors were verified against. Without this check the two drift
// apart silently, and the first symptom is a vector suite failing in CI with no
// explanation on a release nobody has measured.
func TestSOTRNSVersionsCoversTheVersionCIPins(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", ".github", "workflows", "build.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("CI workflow not readable: %v", err)
	}

	// Match the `rns==X.Y.Z` requirement inside a pip install line.
	req := regexp.MustCompile(`rns==([0-9][0-9.]*)`)
	matches := req.FindAllStringSubmatch(string(data), -1)
	if len(matches) == 0 {
		t.Fatalf("no `rns==<version>` pin found in %v; CI installs an unpinned RNS", path)
	}
	found := map[string]bool{}
	for _, m := range matches {
		found[m[1]] = true
		if !slices.Contains(SOTRNSVersions, m[1]) {
			t.Errorf("CI pins rns==%v, which is not one of the verified releases %v; "+
				"re-verify the vectors against it and add it to SOTRNSVersions, or change the CI pin",
				m[1], strings.Join(SOTRNSVersions, " / "))
		}
	}
	t.Logf("CI pins rns %v; verified releases are %v", slices.Sorted(maps.Keys(found)), strings.Join(SOTRNSVersions, " / "))
}
