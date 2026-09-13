// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// RNSVersionFromSource returns the VERSION constant declared in rns/version.go
// of the go-reticulum source this package was compiled with. The file is
// resolved from this package's own location, so it works both in the root
// module and in the cmd/* modules that replace go-reticulum with ../..
//
// Tests compare a built artifact's reported version against this value rather
// than against the rns.VERSION constant: `go test` bakes that constant into the
// test binary before any test builds an artifact, so a version bump landing
// between those two compiles leaves the expectation stale and fails the test
// for a reason that has nothing to do with the artifact.
func RNSVersionFromSource(t *testing.T) string {
	t.Helper()

	path := rnsVersionSourcePath(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %v: %v", path, err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), `const VERSION = "`)
		if !ok {
			continue
		}
		version, ok := strings.CutSuffix(rest, `"`)
		if !ok {
			continue
		}
		return version
	}
	t.Fatalf("no VERSION constant found in %v", path)
	return ""
}

// VersionFlag asserts that the version an artifact reports through its
// --version flag is the version declared in the rns source that artifact was
// compiled from, and returns the probe's raw output.
//
// probe must build the artifact under test and run its --version flag, returning
// the combined output. It is called again, at most three times, while the
// reported version and the source disagree, so a version bump landing between
// the test binary's compile and the artifact's build heals by rebuilding
// instead of failing. name is the program name the flag must prefix the version
// with, as in "gornir 0.109.0" for name "gornir".
func VersionFlag(t *testing.T, name string, probe func(t *testing.T) string) string {
	t.Helper()

	got, want, raw, ok := versionFlag(name,
		func() string { return probe(t) },
		func() string { return RNSVersionFromSource(t) },
	)
	if !ok {
		t.Fatalf("%v --version output = %q, want %q", name, got, want)
	}
	return raw
}

// versionFlag runs the bounded probe loop: it keeps probing while the artifact
// reports a version other than the one the source declares, so a version bump
// landing mid-test heals on the next probe. It returns the last observed output,
// the expected output, the raw probe output, and whether the two agreed.
func versionFlag(name string, probe, sourceVersion func() string) (got, want, raw string, ok bool) {
	for range 3 {
		raw = probe()
		got = strings.TrimSpace(raw)
		want = name + " " + sourceVersion()
		if got == want {
			return got, want, raw, true
		}
	}
	return got, want, raw, false
}

// rnsVersionSourcePath locates rns/version.go beside this package's source
// file. The working-directory fallbacks cover -trimpath builds, where
// runtime.Caller reports an import-path-like string instead of a real path: the
// root module's packages sit one level below the repository root and the cmd/*
// modules two levels below it.
func rnsVersionSourcePath(t *testing.T) string {
	t.Helper()

	var candidates []string
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "..", "rns", "version.go"))
	}
	candidates = append(candidates,
		filepath.Join("..", "rns", "version.go"),
		filepath.Join("..", "..", "rns", "version.go"),
	)
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	t.Fatalf("rns/version.go not found in %v", candidates)
	return ""
}
