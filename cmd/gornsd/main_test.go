// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

func runMainWithArgs(t *testing.T, args ...string) (stdoutText string, stderrText string, exitCode int) {
	t.Helper()
	return runMainWithInput(t, strings.NewReader(""), args...)
}

func runMainWithInput(t *testing.T, stdin io.Reader, args ...string) (stdoutText string, stderrText string, exitCode int) {
	t.Helper()

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	exitCode = run(args, stdin, &stdoutBuf, &stderrBuf)
	return stdoutBuf.String(), stderrBuf.String(), exitCode
}

func TestWaitForInterruptSignalsCallback(t *testing.T) {
	t.Parallel()

	stop := make(chan os.Signal, 1)
	done := make(chan struct{}, 1)
	go func() {
		waitForInterrupt(stop, func() {
			done <- struct{}{}
		})
	}()

	stop <- os.Interrupt

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for interrupt callback")
	}
}

func TestMainVersionOutput(t *testing.T) {
	t.Parallel()
	stdout, stderr, exitCode := runMainWithArgs(t, "--version")
	if exitCode != 0 {
		t.Fatalf("exit code = %v, want 0", exitCode)
	}
	if stdout != "gornsd 0.1.0\n" {
		t.Fatalf("stdout mismatch:\n--- got ---\n%v--- want ---\n%vgornsd 0.1.0\n", stdout, "")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMainExampleConfigOutput(t *testing.T) {
	t.Parallel()
	stdout, stderr, exitCode := runMainWithArgs(t, "--exampleconfig")
	if exitCode != 0 {
		t.Fatalf("exit code = %v, want 0", exitCode)
	}
	if stdout != exampleRNSConfig {
		t.Fatalf("stdout mismatch:\n--- got ---\n%v--- want ---\n%v", stdout, exampleRNSConfig)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMainExampleConfigEndsWithDoubleNewline(t *testing.T) {
	t.Parallel()
	stdout, stderr, exitCode := runMainWithArgs(t, "--exampleconfig")
	if exitCode != 0 {
		t.Fatalf("exit code = %v, want 0", exitCode)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.HasSuffix(stdout, "\n\n") {
		start := len(stdout) - 8
		if start < 0 {
			start = 0
		}
		t.Fatalf("stdout does not end with double newline: %q", stdout[start:])
	}
}

func TestMainExampleConfigNoTrailingWhitespace(t *testing.T) {
	t.Parallel()
	for lineNumber, line := range strings.Split(exampleRNSConfig, "\n") {
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Fatalf("line %v has trailing whitespace: %q", lineNumber+1, line)
		}
	}
}

func TestMainHelpOutput(t *testing.T) {
	t.Parallel()
	stdout, stderr, exitCode := runMainWithArgs(t, "--help")
	if exitCode != 0 {
		t.Fatalf("exit code = %v, want 0", exitCode)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != usageText {
		t.Fatalf("stderr mismatch:\n--- got ---\n%v--- want ---\n%v", stderr, usageText)
	}
}

func TestMainUnknownFlagExitCode2(t *testing.T) {
	t.Parallel()
	stdout, stderr, exitCode := runMainWithArgs(t, "--bogus")
	if exitCode != 2 {
		t.Fatalf("exit code = %v, want 2\nstdout=%q\nstderr=%q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stderr, "flag provided but not defined: -bogus") {
		t.Fatalf("stderr = %q, want flag parser error", stderr)
	}
	if !strings.Contains(stderr, "usage: gornsd") {
		t.Fatalf("stderr = %q, want usage text", stderr)
	}
}

func TestMainInteractiveModeREPL(t *testing.T) {
	t.Parallel()
	configDir, cleanup := testutils.TempDir(t, "gornsd-main-interactive-")
	defer cleanup()
	if err := os.WriteFile(filepath.Join(configDir, "config"), []byte("[reticulum]\nshare_instance = No\n\n[interfaces]\n"), 0o600); err != nil {
		t.Fatalf("write config error: %v", err)
	}

	stdout, stderr, exitCode := runMainWithInput(t, strings.NewReader("version\nquit\n"), "--config", configDir, "-i")
	if exitCode != 0 {
		t.Fatalf("exit code = %v, want 0\nstdout=%q\nstderr=%q", exitCode, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "gornsd 0.1.0") {
		t.Fatalf("stdout = %q, want version output", stdout)
	}
	if !strings.Contains(stdout, "Goodbye.") {
		t.Fatalf("stdout = %q, want goodbye output", stdout)
	}
}
