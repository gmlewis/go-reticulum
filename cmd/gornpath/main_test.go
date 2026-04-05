// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestGornpathNoArgsPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()

	out, err := runGornpath()
	if err != nil {
		t.Fatalf("gornpath with no args failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "usage: gornpath") {
		t.Fatalf("missing usage text: %q", out)
	}
}

func TestGornpathVersionPrintsProgramVersion(t *testing.T) {
	t.Parallel()

	out, err := runGornpath("--version")
	if err != nil {
		t.Fatalf("gornpath --version failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "gornpath ") {
		t.Fatalf("missing version output: %q", out)
	}
}

func runGornpath(args ...string) (string, error) {
	cmdArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}
