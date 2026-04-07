// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"io"
	"os"
	"os/exec"
	"strings"
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

func TestMainUnknownFlagExitCode2(t *testing.T) {
	t.Parallel()

	cmd := exec.Command(os.Args[0], "-test.run=TestMainHelperProcess", "--", "--bogus")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	stdout, stderr, exitCode := runCommand(t, cmd)
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

func TestMainHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for i := range args {
		if args[i] == "--" {
			os.Args = append([]string{"gornsd"}, args[i+1:]...)
			break
		}
	}
	main()
	os.Exit(0)
}

func runCommand(t *testing.T, cmd *exec.Cmd) (stdout string, stderr string, exitCode int) {
	t.Helper()
	stdoutBytes, stderrBytes, err := runCommandOutput(cmd)
	if err == nil {
		return string(stdoutBytes), string(stderrBytes), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return string(stdoutBytes), string(stderrBytes), exitErr.ExitCode()
	}
	t.Fatalf("command failed: %v", err)
	return "", "", 0
}

func runCommandOutput(cmd *exec.Cmd) ([]byte, []byte, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	stdoutBytes, err := io.ReadAll(stdout)
	if err != nil {
		_ = cmd.Wait()
		return nil, nil, err
	}
	stderrBytes, err := io.ReadAll(stderr)
	if err != nil {
		_ = cmd.Wait()
		return nil, nil, err
	}
	return stdoutBytes, stderrBytes, cmd.Wait()
}
