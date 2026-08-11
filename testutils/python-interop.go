// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package testutils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pythonRNSAvailable reports whether a python3 interpreter with the RNS
// package (and its vendored umsgpack) is importable. Cross-implementation
// on-disk storage-format interop tests use this to decide whether to run; they
// skip cleanly on machines without Python RNS installed.
func pythonRNSAvailable() bool {
	cmd := exec.Command("python3", "-c", "import RNS")
	return cmd.Run() == nil
}

// SkipIfNoPythonRNS skips the calling test when Python RNS is not installed.
// Call this at the top of every cross-implementation interop test so the suite
// passes in environments that only have the Go side.
func SkipIfNoPythonRNS(t *testing.T) {
	t.Helper()
	if !pythonRNSAvailable() {
		t.Skip("python3 RNS not available; skipping cross-implementation interop test")
	}
}

// RunPython writes script to a temp .py file under /tmp, runs it with
// `python3 script.py args...`, and returns stdout. A non-zero exit fails the
// test with both stdout and stderr shown. Stray stderr (e.g. RNS log notices)
// is tolerated; only the exit code gates failure, so scripts should drive
// pass/fail via `assert`/`raise` and communicate results via `print`.
func RunPython(t *testing.T, script string, args ...string) string {
	t.Helper()
	dir := TempDir(t, "py-interop-")
	path := filepath.Join(dir, "script.py")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatalf("write python script: %v", err)
	}
	cmd := exec.Command("python3", append([]string{path}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("python3 script failed: %v\n--- stderr ---\n%s\n--- stdout ---\n%s", err, stderr.String(), stdout.String())
	}
	return stdout.String()
}
