// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
	"os"
	"testing"
	"time"
)

func runMainWithArgs(t *testing.T, args ...string) (stdout string, stderr string) {
	t.Helper()

	originalArgs := os.Args
	originalStdout := os.Stdout
	originalStderr := os.Stderr
	t.Cleanup(func() {
		os.Args = originalArgs
		os.Stdout = originalStdout
		os.Stderr = originalStderr
	})

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdout error: %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		t.Fatalf("os.Pipe stderr error: %v", err)
	}

	os.Args = append([]string{"gornsd"}, args...)
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter

	main()

	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()

	stdoutBytes, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("read stdout error: %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("read stderr error: %v", err)
	}

	_ = stdoutReader.Close()
	_ = stderrReader.Close()

	return string(stdoutBytes), string(stderrBytes)
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
	stdout, stderr := runMainWithArgs(t, "--version")
	if stdout != "gornsd 0.1.0\n" {
		t.Fatalf("stdout mismatch:\n--- got ---\n%v--- want ---\n%vgornsd 0.1.0\n", stdout, "")
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMainExampleConfigOutput(t *testing.T) {
	stdout, stderr := runMainWithArgs(t, "--exampleconfig")
	if stdout != exampleRNSConfig {
		t.Fatalf("stdout mismatch:\n--- got ---\n%v--- want ---\n%v", stdout, exampleRNSConfig)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestMainHelpOutput(t *testing.T) {
	stdout, stderr := runMainWithArgs(t, "--help")
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if stderr != usageText {
		t.Fatalf("stderr mismatch:\n--- got ---\n%v--- want ---\n%v", stderr, usageText)
	}
}
