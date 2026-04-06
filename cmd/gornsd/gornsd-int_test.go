// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration && linux
// +build integration,linux

package main

import (
	"bytes"
	"os"
	exec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type cliOutcome struct {
	stdout   string
	stderr   string
	exitCode int
}

func runGornsdOutcome(t *testing.T, args ...string) cliOutcome {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "./cmd/gornsd"}, args...)...)
	cmd.Dir = filepath.Clean(filepath.Join("..", ".."))
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return cliOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitErr.ExitCode()}
		}
		t.Fatalf("gornsd command failed: %v", err)
	}
	return cliOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: 0}
}

func runRnsdOutcome(t *testing.T, args ...string) cliOutcome {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", "original-reticulum-repo"))
	if err != nil {
		t.Fatalf("failed to resolve original repo root: %v", err)
	}
	cmd := exec.Command("python3", append([]string{"RNS/Utilities/rnsd.py"}, args...)...)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "PYTHONPATH="+repoRoot)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return cliOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitErr.ExitCode()}
		}
		t.Fatalf("rnsd command failed: %v", err)
	}
	return cliOutcome{stdout: stdout.String(), stderr: stderr.String(), exitCode: 0}
}

func normalizeHelpOutput(text string) string {
	text = strings.ReplaceAll(text, "gornsd", "rnsd")
	text = strings.ReplaceAll(text, "rnsd.py", "rnsd")
	text = strings.ReplaceAll(text, "Go Reticulum Network Stack Daemon", "Reticulum Network Stack Daemon")
	return strings.Join(strings.Fields(text), " ")
}

func normalizeMultilineWhitespace(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func TestGornsdHelpParity(t *testing.T) {
	t.Parallel()
	got := runGornsdOutcome(t, "--help")
	want := runRnsdOutcome(t, "--help")
	if got.exitCode != want.exitCode {
		t.Fatalf("exit code mismatch: got %v want %v", got.exitCode, want.exitCode)
	}
	if normalizeHelpOutput(got.stderr) != normalizeHelpOutput(want.stdout) {
		t.Fatalf("usage mismatch:\n--- got ---\n%v--- want ---\n%v", got.stderr, want.stdout)
	}
	if got.stdout != "" || want.stderr != "" {
		t.Fatalf("unexpected extra output: got stdout=%q want stderr=%q", got.stdout, want.stderr)
	}
}

func TestGornsdExampleConfigParity(t *testing.T) {
	t.Parallel()
	got := runGornsdOutcome(t, "--exampleconfig")
	want := runRnsdOutcome(t, "--exampleconfig")
	if got.exitCode != want.exitCode {
		t.Fatalf("exit code mismatch: got %v want %v", got.exitCode, want.exitCode)
	}
	if normalizeMultilineWhitespace(got.stderr) != normalizeMultilineWhitespace(want.stderr) {
		t.Fatalf("stderr mismatch:\n--- got ---\n%v--- want ---\n%v", got.stderr, want.stderr)
	}
	if normalizeMultilineWhitespace(got.stdout) != normalizeMultilineWhitespace(want.stdout) {
		t.Fatalf("stdout mismatch:\n--- got ---\n%v--- want ---\n%v", got.stdout, want.stdout)
	}
}

func TestGornsdVersionOutputs(t *testing.T) {
	t.Parallel()
	got := runGornsdOutcome(t, "--version")
	want := runRnsdOutcome(t, "--version")
	if got.exitCode != 0 || want.exitCode != 0 {
		t.Fatalf("version command exit codes: got %v want %v", got.exitCode, want.exitCode)
	}
	if got.stderr != "" || want.stderr != "" {
		t.Fatalf("version stderr mismatch: got %q want %q", got.stderr, want.stderr)
	}
	if got.stdout != "gornsd 0.1.0\n" {
		t.Fatalf("gornsd stdout = %q, want %q", got.stdout, "gornsd 0.1.0\n")
	}
	if want.stdout != "rnsd 1.1.4\n" {
		t.Fatalf("rnsd stdout = %q, want %q", want.stdout, "rnsd 1.1.4\n")
	}
}
