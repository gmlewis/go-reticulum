// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// sinkEvent is one SetSinkErrorFunc delivery.
type sinkEvent struct {
	op  string
	err error
}

// collectSinkEvents records sink events. The logger delivers them on its
// writer goroutine; callers read after Flush or Close, which orders the
// delivery before the read.
func collectSinkEvents(events *[]sinkEvent) func(op string, err error) {
	return func(op string, err error) {
		*events = append(*events, sinkEvent{op: op, err: err})
	}
}

// TestLoggerConsoleFallbackDisabledNoConsole verifies that a terminal UI can
// turn off the Python console dump: a broken file sink must not write a single
// byte to the standard logger, must not latch the override, and must report the
// failure instead.
func TestLoggerConsoleFallbackDisabledNoConsole(t *testing.T) {
	dir := testutils.TempDir(t, "logger-sink-noconsole-")
	path := filepath.Join(dir, "logfile")
	// A directory where the log file belongs makes every open fail.
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	oldWriter := log.Writer()
	var console bytes.Buffer
	log.SetOutput(&console)
	t.Cleanup(func() { log.SetOutput(oldWriter) })

	var events []sinkEvent
	logger := NewLogger()
	logger.SetLogLevel(LogInfo)
	logger.SetLogDest(LogDestFile)
	logger.SetLogFilePath(path)
	logger.SetConsoleFallback(false)
	logger.SetSinkErrorFunc(collectSinkEvents(&events))
	logger.Info("first line")
	logger.Info("second line")
	if !logger.Flush() {
		t.Fatal("Flush() timed out")
	}

	if got := console.String(); got != "" {
		t.Fatalf("console output = %q, want none with the console fallback disabled", got)
	}
	if logger.GetAlwaysOverride() {
		t.Fatal("override latched although the console fallback is disabled")
	}
	if len(events) != 2 {
		t.Fatalf("sink events = %v, want 2", events)
	}
	for i, ev := range events {
		if ev.op != LogSinkOpening || ev.err == nil {
			t.Fatalf("event %v = {%q, %v}, want {%q, non-nil}", i, ev.op, ev.err, LogSinkOpening)
		}
	}
	logger.Close()
}

// TestLoggerConsoleFallbackEnabledKeepsPythonParity verifies the default
// destination of a failed sink is still the console, with the two Python RNS
// critical lines (RNS/__init__.py log()).
func TestLoggerConsoleFallbackEnabledKeepsPythonParity(t *testing.T) {
	dir := testutils.TempDir(t, "logger-sink-console-")
	path := filepath.Join(dir, "logfile")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	oldWriter := log.Writer()
	var console bytes.Buffer
	log.SetOutput(&console)
	t.Cleanup(func() { log.SetOutput(oldWriter) })

	logger := NewLogger()
	logger.SetLogLevel(LogInfo)
	logger.SetLogDest(LogDestFile)
	logger.SetLogFilePath(path)
	if !logger.GetConsoleFallback() {
		t.Fatal("GetConsoleFallback() = false, want true by default")
	}
	logger.Info("learned a path")
	if !logger.Flush() {
		t.Fatal("Flush() timed out")
	}

	out := console.String()
	for _, want := range []string{
		"Exception occurred while writing log message to log file",
		"Dumping future log events to console!",
		"learned a path",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("console output %q missing %q", out, want)
		}
	}
	if !logger.GetAlwaysOverride() {
		t.Fatal("GetAlwaysOverride() = false, want true after a failed file sink")
	}
	logger.Close()
}

// TestLoggerSinkRecoveryReportedOnce verifies the sink error callback is
// cleared once the file works again, so a transient failure does not look
// permanent to the caller measuring it.
func TestLoggerSinkRecoveryReportedOnce(t *testing.T) {
	dir := testutils.TempDir(t, "logger-sink-recover-")
	path := filepath.Join(dir, "logfile")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	var events []sinkEvent
	logger := NewLogger()
	logger.SetLogLevel(LogInfo)
	logger.SetLogDest(LogDestFile)
	logger.SetLogFilePath(path)
	logger.SetConsoleFallback(false)
	logger.SetSinkErrorFunc(collectSinkEvents(&events))

	logger.Info("while broken")
	if !logger.Flush() {
		t.Fatal("Flush() timed out")
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	logger.Info("after recovery")
	logger.Info("and again")
	if !logger.Flush() {
		t.Fatal("Flush() timed out")
	}

	var recoveries int
	for _, ev := range events {
		if ev.err == nil {
			recoveries++
		}
	}
	if recoveries != 1 {
		t.Fatalf("recovery events = %v in %v, want exactly 1", recoveries, events)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "after recovery") || !strings.Contains(string(data), "and again") {
		t.Fatalf("log file %q missing the lines written after recovery", data)
	}
	logger.Close()
}

// TestLoggerRotatesFullFile verifies a log file over LogMaxSize is moved to
// .1 and writing continues in a fresh file. On Windows this only passes when
// the write handle is closed before the rename.
func TestLoggerRotatesFullFile(t *testing.T) {
	dir := testutils.TempDir(t, "logger-sink-rotate-")
	path := filepath.Join(dir, "logfile")
	filler := bytes.Repeat([]byte("x"), LogMaxSize+1)
	if err := os.WriteFile(path, filler, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	logger := NewLogger()
	logger.SetLogLevel(LogInfo)
	logger.SetLogDest(LogDestFile)
	logger.SetLogFilePath(path)
	// Python writes the line first and rotates afterwards, so the triggering
	// line lands in the previous file and the next line starts a fresh one.
	logger.Info("trigger rotation")
	logger.Info("after rotation")
	if !logger.Flush() {
		t.Fatal("Flush() timed out")
	}
	logger.Close()

	prev, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("previous log file: %v", err)
	}
	if !strings.Contains(string(prev), "trigger rotation") {
		t.Fatalf("previous log file does not contain the triggering line")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "after rotation") {
		t.Fatalf("log file %q does not contain the line written after rotation", data)
	}
	if got := len(data); got >= len(filler) {
		t.Fatalf("log file still %v bytes, want a fresh file", got)
	}
}

// TestLoggerRotationFailureReportedOnce verifies a rotation that cannot
// succeed is not retried on every later line: the failure is reported once and
// logging continues into the same file.
func TestLoggerRotationFailureReportedOnce(t *testing.T) {
	dir := testutils.TempDir(t, "logger-sink-rotatefail-")
	path := filepath.Join(dir, "logfile")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), LogMaxSize+1), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// A non-empty directory at <path>.1 makes both the removal and the rename
	// fail on every platform.
	if err := os.Mkdir(path+".1", 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path+".1", "keep"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var rotating int
	logger := NewLogger()
	logger.SetLogLevel(LogInfo)
	logger.SetLogDest(LogDestFile)
	logger.SetLogFilePath(path)
	logger.SetConsoleFallback(false)
	logger.SetSinkErrorFunc(func(op string, err error) {
		if op == LogSinkRotating && err != nil {
			rotating++
		}
	})
	logger.Info("one")
	logger.Info("two")
	logger.Info("three")
	if !logger.Flush() {
		t.Fatal("Flush() timed out")
	}
	logger.Close()

	if rotating != 1 {
		t.Fatalf("rotation failures reported = %v, want 1", rotating)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, want := range []string{"one", "two", "three"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("log file %q missing %q", data, want)
		}
	}
}
