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

func TestGornprobeNoArgsPrintsUsageAndExitsZero(t *testing.T) {
	t.Parallel()

	out, err := runGornprobe()
	if err != nil {
		t.Fatalf("gornprobe with no args failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "usage: gornprobe") {
		t.Fatalf("missing usage text: %q", out)
	}
}

func TestGornprobeVersionPrintsProgramVersion(t *testing.T) {
	t.Parallel()

	out, err := runGornprobe("--version")
	if err != nil {
		t.Fatalf("gornprobe --version failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "gornprobe ") {
		t.Fatalf("missing version output: %q", out)
	}
}

func TestGornprobeInvalidHashLengthExitsZero(t *testing.T) {
	t.Parallel()

	out, err := runGornprobe("gornprobe.debug", "001122")
	if err != nil {
		t.Fatalf("gornprobe invalid hash length failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "Destination length is invalid, must be 32 hexadecimal characters (16 bytes).") {
		t.Fatalf("missing hash length error: %q", out)
	}
}

func TestGornprobeInvalidHashHexExitsZero(t *testing.T) {
	t.Parallel()

	out, err := runGornprobe("gornprobe.debug", strings.Repeat("z", 32))
	if err != nil {
		t.Fatalf("gornprobe invalid hash hex failed: %v\n%v", err, out)
	}
	if !strings.Contains(out, "Invalid destination entered. Check your input.") {
		t.Fatalf("missing hash hex error: %q", out)
	}
}

func runGornprobe(args ...string) (string, error) {
	cmdArgs := append([]string{"run", "."}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	return string(out), err
}
