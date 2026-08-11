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

	tmpDir := testutils.TempDir(t, "logger-test-defaults-")
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

	tmpDir := testutils.TempDir(t, "logger-test-")

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

// TestLogPathingLevelAndLabel verifies the v1.3.9/1.4.1 log-level additions
// (RNS/__init__.py:73-74,106-107): LogPathing=7 sits between LogDebug and the
// bumped LogExtreme=8, with the "[Pathing] " label.
func TestLogPathingLevelAndLabel(t *testing.T) {
	t.Parallel()
	if LogPathing != 7 {
		t.Fatalf("LogPathing = %d, want 7", LogPathing)
	}
	if LogExtreme != 8 {
		t.Fatalf("LogExtreme = %d, want 8", LogExtreme)
	}
	if got := LogLevelName(LogPathing); got != "[Pathing] " {
		t.Fatalf("LogLevelName(LogPathing) = %q, want %q", got, "[Pathing] ")
	}
	if got := LogLevelName(LogExtreme); got != "[Extra]   " {
		t.Fatalf("LogLevelName(LogExtreme) = %q, want %q", got, "[Extra]   ")
	}
}

// TestSetLogLevelClampsToLogExtreme verifies the loglevel clamp ceiling was
// bumped from 7 to LogExtreme (8) alongside the new LogPathing level
// (RNS/Reticulum.py:306, v1.4.1).
func TestSetLogLevelClampsToLogExtreme(t *testing.T) {
	t.Parallel()
	logger := NewLogger()
	logger.SetPendingDelta(100) // push the effective level past the ceiling
	logger.SetLogLevel(LogNotice)
	if got := logger.GetLogLevel(); got != LogExtreme {
		t.Fatalf("clamped loglevel = %d, want %d (LogExtreme)", got, LogExtreme)
	}

	low := NewLogger()
	low.SetPendingDelta(-100) // push below the floor
	low.SetLogLevel(LogNotice)
	if got := low.GetLogLevel(); got != LogCritical {
		t.Fatalf("clamped loglevel = %d, want %d (LogCritical)", got, LogCritical)
	}
}

// TestLogTimestampsGatesPrefix verifies the logtimestamps config
// (RNS/__init__.py:86,133 / RNS/Reticulum.py:463-465, v1.3.2): the timestamp
// prefix is present by default and omitted when SetLogTimestamps(false).
func TestLogTimestampsGatesPrefix(t *testing.T) {
	t.Parallel()

	// Default: timestamps enabled → output is "[<timestamp>] [Notice]  msg".
	withTS := NewLogger()
	withTS.SetLogLevel(LogExtreme)
	var cbTS bytes.Buffer
	withTS.SetLogDest(LogCallback)
	withTS.SetLogCallback(func(msg string) { cbTS.WriteString(msg) })
	withTS.Notice("with-timestamp")
	outTS := cbTS.String()
	if !strings.Contains(outTS, "] [Notice]") {
		t.Fatalf("timestamps enabled: output %q should contain a timestamp before the level label", outTS)
	}
	if !strings.Contains(outTS, "with-timestamp") {
		t.Fatalf("output missing message: %q", outTS)
	}

	// Disabled: output omits the timestamp prefix → "[Notice]  msg".
	withoutTS := NewLogger()
	withoutTS.SetLogLevel(LogExtreme)
	withoutTS.SetLogTimestamps(false)
	var cbNoTS bytes.Buffer
	withoutTS.SetLogDest(LogCallback)
	withoutTS.SetLogCallback(func(msg string) { cbNoTS.WriteString(msg) })
	withoutTS.Notice("no-timestamp")
	outNoTS := cbNoTS.String()
	if !strings.HasPrefix(outNoTS, "[Notice]") {
		t.Fatalf("timestamps disabled: output %q should start with the level label, not a timestamp", outNoTS)
	}
	if strings.Contains(outNoTS, "] [Notice]") {
		t.Fatalf("timestamps disabled: output %q unexpectedly contains a timestamp prefix", outNoTS)
	}
	if !strings.Contains(outNoTS, "no-timestamp") {
		t.Fatalf("output missing message: %q", outNoTS)
	}
}
