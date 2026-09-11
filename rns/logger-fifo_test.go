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
//
// Assertions are state-based, not throughput-based. A wall-clock budget for N
// calls is CI-load-sensitive and does not pin the property under test:
// non-blocking is proved by (1) the first call returning while the sink is
// stuck and (2) every post-full call taking the drop path — DroppedCount
// advances by exactly one per call, with the writer unable to drain.
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

	// Hang detector (not a throughput budget): a single log call must return
	// even though the sink's open blocks forever. Failure means logging is
	// coupled to the hot path, not that the machine is slow.
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

	// Fill past the queue. The writer is stuck in the FIFO open and cannot
	// drain, so after LogQueueDepth successful enqueues every further call
	// must drop. The +8 covers the race where the writer has not yet dequeued
	// the first Notice.
	for i := range LogQueueDepth + 8 {
		logger.Debug("fill line %d", i)
	}
	base := logger.DroppedCount()
	if base == 0 {
		t.Fatal("no overflow drops after filling the log queue — callers were blocked instead of dropping")
	}

	// Every call after the queue is full must drop immediately (this loop
	// finishing is the non-blocking proof) and must advance DroppedCount by
	// exactly one — no silent loss, no waiting on the stuck writer.
	const postFull = 64
	for i := range postFull {
		logger.Debug("post-full line %d", i)
	}
	if got, want := logger.DroppedCount(), base+postFull; got != want {
		t.Fatalf("DroppedCount = %v, want %v (each post-full call must drop, not block)", got, want)
	}
}
