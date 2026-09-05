// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !windows

package rns

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestLoggerLogNeverBlocksOnFileIO pins the hot-path decoupling: a log call
// must never wait on the sink's file I/O. The transport's packet goroutines
// log two-to-four Debug lines per packet; when the sink is slow (an SD card
// mid-flush), a synchronous logger serializes the entire inbound pipeline on
// the file open/write/stat/close and backs up packet delivery by tens of
// seconds — the live raspberrypi flapping mechanism.
//
// The writer is blocked deterministically with a FIFO that has no reader: the
// sink's open call blocks until a reader appears, so every queued line sits in
// the queue instead of the caller's goroutine. log() must enqueue without
// blocking, count the overflow drops, and keep serving callers.
func TestLoggerLogNeverBlocksOnFileIO(t *testing.T) {
	tmpDir := testutils.TempDir(t, "logger-fifo-")
	fifoPath := filepath.Join(tmpDir, "logfile")
	if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}
	// No reader is ever opened: the sink stays blocked for the whole test.
	// The spawned writer goroutine leaks for the test's lifetime by design.

	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogFilePath(fifoPath)
	logger.SetLogDest(LogDestFile)

	// A single log call must return promptly even though the sink's open
	// blocks forever.
	done := make(chan struct{})
	go func() {
		logger.Notice("first line into a stuck sink")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("log() blocked on sink I/O — logging is coupled to the hot path")
	}

	// A sustained burst must keep returning promptly and overflow must be
	// counted, not absorbed by blocking the callers.
	start := time.Now()
	const burst = 50000
	for i := range burst {
		logger.Debug("burst line %d", i)
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("%v log calls took %v with a stuck sink — the hot path blocked", burst, elapsed)
	}
	if got := logger.DroppedCount(); got == 0 {
		t.Fatal("no overflow drops were counted while the sink was stuck")
	}
}
