// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// runGit runs a git command in dir and returns stdout, failing the test
// on error. Shared by the non-integration release tests and the
// integration test suite.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s in %s failed: %v\nstderr: %s", strings.Join(args, " "), dir, err, stderr.String())
	}
	return stdout.String()
}

// firstByte returns the first byte of b, or 0xFF when empty. Shared by the
// non-integration work tests and the integration test suite.
func firstByte(b []byte) byte {
	if len(b) == 0 {
		return 0xFF
	}
	return b[0]
}
