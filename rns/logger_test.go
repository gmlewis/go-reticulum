// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func TestNewLoggerDefaultsAndMutation(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	if got := logger.GetLogLevel(); got != LogNotice {
		t.Fatalf("GetLogLevel() = %v, want %v", got, LogNotice)
	}
	if got := logger.GetLogDest(); got != LogStdout {
		t.Fatalf("GetLogDest() = %v, want %v", got, LogStdout)
	}
	if logger.GetAlwaysOverride() {
		t.Fatal("GetAlwaysOverride() = true, want false")
	}
	if logger.GetCompactLogFmt() {
		t.Fatal("GetCompactLogFmt() = true, want false")
	}
	if got := logger.GetLogFilePath(); got != "" {
		t.Fatalf("GetLogFilePath() = %q, want empty", got)
	}
	if got := logger.GetLogCallback(); got != nil {
		t.Fatal("GetLogCallback() = non-nil, want nil")
	}

	tmpDir, cleanup := testutils.TempDir(t, "logger-test-defaults-")
	defer cleanup()
	logPath := filepath.Join(tmpDir, "logfile")

	logger.SetAlwaysOverride(true)
	logger.SetCompactLogFmt(true)
	logger.SetLogLevel(LogDebug)
	logger.SetLogFilePath(logPath)
	logger.SetLogDest(LogDestFile)

	var callbackCalled bool
	logger.SetLogCallback(func(msg string) {
		callbackCalled = msg == "hello"
	})

	if !logger.GetAlwaysOverride() {
		t.Fatal("GetAlwaysOverride() = false, want true")
	}
	if !logger.GetCompactLogFmt() {
		t.Fatal("GetCompactLogFmt() = false, want true")
	}
	if got := logger.GetLogLevel(); got != LogDebug {
		t.Fatalf("GetLogLevel() = %v, want %v", got, LogDebug)
	}
	if got := logger.GetLogFilePath(); got != logPath {
		t.Fatalf("GetLogFilePath() = %q, want %q", got, logPath)
	}
	if got := logger.GetLogDest(); got != LogDestFile {
		t.Fatalf("GetLogDest() = %v, want %v", got, LogDestFile)
	}
	if got := logger.GetLogCallback(); got == nil {
		t.Fatal("GetLogCallback() = nil, want function")
	} else {
		got("hello")
	}
	if !callbackCalled {
		t.Fatal("callback was not called")
	}
}

func TestNewLoggerWritesToCallbackAndFile(t *testing.T) {
	t.Parallel()

	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)

	var callback bytes.Buffer
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(msg string) {
		callback.WriteString(msg)
	})
	logger.Notice("callback message")
	if got, want := callback.String(), "["; !strings.HasPrefix(got, want) || !strings.Contains(got, "callback message") {
		t.Fatalf("callback output = %q, want message containing %q", got, "callback message")
	}

	tmpDir, cleanup := testutils.TempDir(t, "logger-test-")
	defer cleanup()

	logPath := filepath.Join(tmpDir, "logfile")
	logger.SetLogFilePath(logPath)
	logger.SetLogDest(LogDestFile)
	logger.Notice("file message")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(string(data), "file message") {
		t.Fatalf("logfile output = %q, want message containing %q", string(data), "file message")
	}
}
