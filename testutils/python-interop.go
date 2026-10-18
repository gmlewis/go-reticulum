// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// SOTRNSVersions are the RNS releases the Python-derived vector suites have been
// verified against: 1.4.2, the release the port was written against and the one
// a plain `python3` resolves to on this machine, and 1.5.0, which CI installs.
// Both are accepted because both were measured; anything else is not the
// reference the expectations were derived from, so a run against it fails
// loudly rather than passing quietly on values nobody checked.
//
// Bump or extend this set in the same change that re-verifies the affected
// vectors against the new release.
var SOTRNSVersions = []string{"1.4.2", "1.5.0"}

// allowRNSDriftEnv downgrades a reference-version mismatch from a test failure
// to a logged warning when set to a non-empty value, for the deliberate case of
// comparing against another release.
const allowRNSDriftEnv = "GORETICULUM_ALLOW_RNS_DRIFT"

// rnsVersionProbeScript prints one SOT:-prefixed JSON line describing the
// installed RNS reference. The SOT: prefix, and taking the LAST such line, exist
// because importing RNS can write to stdout — RNS logs there by default — which
// would otherwise corrupt a bare JSON document. RNS.loglevel is set to LOG_NONE
// first for the same reason.
const rnsVersionProbeScript = `
import json
import RNS
RNS.loglevel = RNS.LOG_NONE
print("SOT:" + json.dumps({"rns": RNS.__version__}))
`

// pythonRNSCandidates are the interpreters probed for an importable RNS
// reference, in preference order. The system `python3` comes first so a
// venv/PATH python wins; the Homebrew py3.14 interpreter is the macOS fallback
// that carries the user-site RNS. Adding candidates is safe — the first that can
// import RNS and report its version is used.
var pythonRNSCandidates = []string{
	"python3",
	"/opt/homebrew/bin/python3",
	"python3.14",
	"python3.13",
}

var (
	pythonRNSOnce sync.Once
	pythonRNSExe  string
	pythonRNSVer  string
)

// pythonRNSReference returns the probed RNS interpreter and the version it
// reports, or ("", "") when no candidate can import RNS. The probe runs at most
// once per test binary.
func pythonRNSReference() (string, string) {
	pythonRNSOnce.Do(func() {
		for _, exe := range pythonRNSCandidates {
			out, err := exec.Command(exe, "-c", rnsVersionProbeScript).Output()
			if err != nil {
				continue
			}
			if v, ok := parseRNSSOTLine(out); ok {
				pythonRNSExe, pythonRNSVer = exe, v
				return
			}
		}
	})
	return pythonRNSExe, pythonRNSVer
}

// parseRNSSOTLine decodes the last SOT:-prefixed line of out, ignoring any other
// stdout the reference emitted while importing.
func parseRNSSOTLine(out []byte) (string, bool) {
	version := ""
	found := false
	for line := range strings.SplitSeq(string(out), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "SOT:")
		if !ok {
			continue
		}
		var probe struct {
			RNS string `json:"rns"`
		}
		if err := json.Unmarshal([]byte(rest), &probe); err != nil || probe.RNS == "" {
			continue
		}
		version, found = probe.RNS, true
	}
	return version, found
}

// PythonRNSVersion returns the RNS version reported by the probed reference, or
// "" when no reference is importable. Tests that need to vary an expectation by
// reference release read it here rather than re-probing.
func PythonRNSVersion() string {
	_, v := pythonRNSReference()
	return v
}

// SkipIfNoPythonRNS skips the calling test when Python RNS is not installed.
// Call this at the top of every cross-implementation interop test so the suite
// passes in environments that only have the Go side.
//
// It also ENFORCES the reference version pin (SOTRNSVersions): when RNS is
// importable but is not one of the verified releases, the test fails rather than
// skipping or quietly proceeding. These suites derive their expectations by
// running the Python reference, so a comparison against an unverified release
// asserts behaviour nobody has checked and is indistinguishable from a port
// defect. The message names both the found and the accepted versions; set
// GORETICULUM_ALLOW_RNS_DRIFT=1 to downgrade it to a warning when comparing
// against another release on purpose.
func SkipIfNoPythonRNS(t *testing.T) {
	t.Helper()
	exe, version := pythonRNSReference()
	if exe == "" {
		t.Skip("python3 RNS not available; skipping cross-implementation interop test")
	}
	AssertSOTRNSVersion(t, exe, version)
}

// AssertSOTRNSVersion fails t unless version is one of the RNS releases these
// suites were verified against. It is exported so tests can pin the rule itself
// with a synthetic version.
func AssertSOTRNSVersion(t testing.TB, exe, version string) {
	t.Helper()
	if slices.Contains(SOTRNSVersions, version) {
		return
	}
	msg := fmt.Sprintf("python RNS reference at %v is RNS %v, but these vectors are verified against RNS %v",
		exe, version, strings.Join(SOTRNSVersions, " / "))
	if os.Getenv(allowRNSDriftEnv) != "" {
		t.Logf("WARNING: %v (comparison allowed by %v)", msg, allowRNSDriftEnv)
		return
	}
	t.Fatalf("%v\nA vector run against an unverified release asserts values nobody has checked.\n"+
		"Install a verified release, re-verify the vectors and add the release to SOTRNSVersions, "+
		"or set %v=1 to compare against this one deliberately.", msg, allowRNSDriftEnv)
}

// PythonRRCDRepoDir resolves the original Python rrcd repo directory: the
// original-rrcd-repo symlink found by walking up from the current directory,
// falling back to ~/src/github.com/kc1awv/rrcd. The calling test skips when
// neither exists. Scripts receive it as a sys.argv entry or through direct
// Go string concatenation — no absolute path is ever embedded in a script.
func PythonRRCDRepoDir(t *testing.T) string {
	t.Helper()
	if dir, err := os.Getwd(); err == nil {
		for {
			p := filepath.Join(dir, "original-rrcd-repo")
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				if fi, err := os.Stat(resolved); err == nil && fi.IsDir() {
					return resolved
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	home, err := os.UserHomeDir()
	if err == nil {
		p := filepath.Join(home, "src", "github.com", "kc1awv", "rrcd")
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
	}
	t.Skipf("original Python rrcd repo not found (no original-rrcd-repo symlink and no ~/src/github.com/kc1awv/rrcd)")
	return "" // unreachable
}

// RunPython writes script to a temp .py file under /tmp, runs it with the probed
// RNS interpreter, and returns stdout. A non-zero exit fails the test with both
// stdout and stderr shown. Stray stderr (e.g. RNS log notices) is tolerated; only
// the exit code gates failure, so scripts should drive pass/fail via
// `assert`/`raise` and communicate results via `print`.
//
// The interpreter comes from the same probe SkipIfNoPythonRNS pins, so the
// version a script runs under is the version the test was allowed to run
// against rather than whatever `python3` happens to be.
func RunPython(t *testing.T, script string, args ...string) string {
	t.Helper()
	exe, _ := pythonRNSReference()
	if exe == "" {
		exe = "python3"
	}
	dir := TempDir(t, "py-interop-")
	path := filepath.Join(dir, "script.py")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}
	cmd := exec.Command(exe, append([]string{path}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python3 script failed: %v\n--- stderr ---\n%v\n--- stdout ---\n%v", err, stderr.String(), stdout.String())
	}
	return stdout.String()
}
