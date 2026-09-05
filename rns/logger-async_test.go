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
	"strconv"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestLoggerFlushDrainsToFile verifies that Flush() waits for every line
// already submitted to reach the sink, in submission order.
func TestLoggerFlushDrainsToFile(t *testing.T) {
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)

	tmpDir := testutils.TempDir(t, "logger-flush-")
	logPath := filepath.Join(tmpDir, "logfile")
	logger.SetLogFilePath(logPath)
	logger.SetLogDest(LogDestFile)

	const lines = 500
	for i := range lines {
		logger.Debug("flush line %d", i)
	}
	if !logger.Flush() {
		t.Fatal("Flush() timed out waiting for the writer to drain")
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	for i := range lines {
		if !strings.Contains(got, "flush line "+strconv.Itoa(i)+"\n") {
			t.Fatalf("flushed logfile missing %q", "flush line "+strconv.Itoa(i))
		}
	}
	// Order must be preserved: the last written line is the last submitted.
	if !strings.HasSuffix(got, "flush line "+strconv.Itoa(lines-1)+"\n") {
		t.Fatalf("flushed logfile does not end with the last submitted line: %q", got[len(got)-80:])
	}
}

// TestLoggerCloseStopsWriterAndFallsBack verifies Close drains queued lines,
// stops the writer goroutine, is idempotent, and routes post-close messages to
// the standard-log fallback instead of panicking on a stopped queue.
func TestLoggerCloseStopsWriterAndFallsBack(t *testing.T) {
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)

	tmpDir := testutils.TempDir(t, "logger-close-")
	logPath := filepath.Join(tmpDir, "logfile")
	logger.SetLogFilePath(logPath)
	logger.SetLogDest(LogDestFile)

	logger.Notice("before close")
	if !logger.Flush() {
		t.Fatal("Flush() timed out before Close")
	}

	// Capture the standard-log fallback (the post-close sink).
	var fallback bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&fallback)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags) })

	logger.Close()
	logger.Close() // idempotent

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "before close") {
		t.Fatalf("closed logfile missing the queued message: %q", string(data))
	}

	logger.Notice("after close")
	if !strings.Contains(fallback.String(), "after close") {
		t.Fatalf("post-close message did not reach the fallback: %q", fallback.String())
	}
}
