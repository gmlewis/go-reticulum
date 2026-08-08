// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

// Benchmarks for the Logger type.
//
// The suite measures logging throughput under multiple axes:
//   - Destination:    /dev/null (I/O sink), /tmp file (real filesystem)
//   - Level:          each of the 8 log levels (Critical..Extreme)
//   - Format:         normal vs compact
//   - Timestamp:      standard vs precise
//   - Concurrency:    single-goroutine vs parallel
//   - Message length: short vs long
//   - Filtered path:  LogNone and below-threshold early returns
//   - Setters:        reconfiguration cost
//
// All benchmarks call logger.log directly with a constant message string to
// avoid measuring fmt.Sprintf throughput — we are benchmarking the Logger
// implementation, not the Go standard library.

const benchMsg = "benchmark log message"

const benchLongMsg = "this is a significantly longer log message that exercises the formatter with more characters to format and write to the output sink on every iteration"

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func benchLoggerDevNull(_ *testing.B, level int, compact bool) *Logger {
	logger := NewLogger()
	logger.SetLogLevel(level)
	logger.SetCompactLogFmt(compact)
	logger.SetLogDest(LogDestFile)
	logger.SetLogFilePath("/dev/null")
	return logger
}

func benchLoggerTempFile(b *testing.B, level int, compact bool) (*Logger, string) {
	b.Helper()
	tmpDir := testutils.TempDirBench(b, "logger-bench-")

	logPath := filepath.Join(tmpDir, "bench.log")
	logger := NewLogger()
	logger.SetLogLevel(level)
	logger.SetCompactLogFmt(compact)
	logger.SetLogDest(LogDestFile)
	logger.SetLogFilePath(logPath)
	return logger, logPath
}

// ---------------------------------------------------------------------------
// Destination: /dev/null — all 8 levels, normal format, standard timestamp
// ---------------------------------------------------------------------------

func BenchmarkLoggerDevNullCritical(b *testing.B) {
	logger := benchLoggerDevNull(b, LogCritical, false)

	for b.Loop() {
		logger.log(benchMsg, LogCritical, false)
	}
}

func BenchmarkLoggerDevNullError(b *testing.B) {
	logger := benchLoggerDevNull(b, LogError, false)

	for b.Loop() {
		logger.log(benchMsg, LogError, false)
	}
}

func BenchmarkLoggerDevNullWarning(b *testing.B) {
	logger := benchLoggerDevNull(b, LogWarning, false)

	for b.Loop() {
		logger.log(benchMsg, LogWarning, false)
	}
}

func BenchmarkLoggerDevNullNotice(b *testing.B) {
	logger := benchLoggerDevNull(b, LogNotice, false)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, false)
	}
}

func BenchmarkLoggerDevNullInfo(b *testing.B) {
	logger := benchLoggerDevNull(b, LogInfo, false)

	for b.Loop() {
		logger.log(benchMsg, LogInfo, false)
	}
}

func BenchmarkLoggerDevNullVerbose(b *testing.B) {
	logger := benchLoggerDevNull(b, LogVerbose, false)

	for b.Loop() {
		logger.log(benchMsg, LogVerbose, false)
	}
}

func BenchmarkLoggerDevNullDebug(b *testing.B) {
	logger := benchLoggerDevNull(b, LogDebug, false)

	for b.Loop() {
		logger.log(benchMsg, LogDebug, false)
	}
}

func BenchmarkLoggerDevNullExtreme(b *testing.B) {
	logger := benchLoggerDevNull(b, LogExtreme, false)

	for b.Loop() {
		logger.log(benchMsg, LogExtreme, false)
	}
}

// ---------------------------------------------------------------------------
// Destination: /dev/null — normal vs compact format (Notice level)
// ---------------------------------------------------------------------------

func BenchmarkLoggerDevNullCompactNotice(b *testing.B) {
	logger := benchLoggerDevNull(b, LogNotice, true)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, false)
	}
}

// ---------------------------------------------------------------------------
// Destination: /dev/null — standard vs precise timestamp (Notice level)
// ---------------------------------------------------------------------------

func BenchmarkLoggerDevNullPreciseTimestamp(b *testing.B) {
	logger := benchLoggerDevNull(b, LogNotice, false)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, true)
	}
}

// ---------------------------------------------------------------------------
// Destination: /tmp file — all 8 levels, normal format, standard timestamp
// ---------------------------------------------------------------------------

func BenchmarkLoggerFileCritical(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogCritical, false)

	for b.Loop() {
		logger.log(benchMsg, LogCritical, false)
	}
}

func BenchmarkLoggerFileError(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogError, false)

	for b.Loop() {
		logger.log(benchMsg, LogError, false)
	}
}

func BenchmarkLoggerFileWarning(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogWarning, false)

	for b.Loop() {
		logger.log(benchMsg, LogWarning, false)
	}
}

func BenchmarkLoggerFileNotice(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogNotice, false)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, false)
	}
}

func BenchmarkLoggerFileInfo(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogInfo, false)

	for b.Loop() {
		logger.log(benchMsg, LogInfo, false)
	}
}

func BenchmarkLoggerFileVerbose(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogVerbose, false)

	for b.Loop() {
		logger.log(benchMsg, LogVerbose, false)
	}
}

func BenchmarkLoggerFileDebug(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogDebug, false)

	for b.Loop() {
		logger.log(benchMsg, LogDebug, false)
	}
}

func BenchmarkLoggerFileExtreme(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogExtreme, false)

	for b.Loop() {
		logger.log(benchMsg, LogExtreme, false)
	}
}

// ---------------------------------------------------------------------------
// Destination: /tmp file — normal vs compact format (Notice level)
// ---------------------------------------------------------------------------

func BenchmarkLoggerFileCompactNotice(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogNotice, true)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, false)
	}
}

// ---------------------------------------------------------------------------
// Destination: /tmp file — standard vs precise timestamp (Notice level)
// ---------------------------------------------------------------------------

func BenchmarkLoggerFilePreciseTimestamp(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogNotice, false)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, true)
	}
}

// ---------------------------------------------------------------------------
// Message length: short vs long — /dev/null
// ---------------------------------------------------------------------------

func BenchmarkLoggerDevNullShortMsg(b *testing.B) {
	logger := benchLoggerDevNull(b, LogNotice, false)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, false)
	}
}

func BenchmarkLoggerDevNullLongMsg(b *testing.B) {
	logger := benchLoggerDevNull(b, LogNotice, false)

	for b.Loop() {
		logger.log(benchLongMsg, LogNotice, false)
	}
}

// ---------------------------------------------------------------------------
// Message length: short vs long — /tmp file
// ---------------------------------------------------------------------------

func BenchmarkLoggerFileShortMsg(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogNotice, false)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, false)
	}
}

func BenchmarkLoggerFileLongMsg(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogNotice, false)

	for b.Loop() {
		logger.log(benchLongMsg, LogNotice, false)
	}
}

// ---------------------------------------------------------------------------
// Filtered paths — early-return fast paths
// ---------------------------------------------------------------------------

func BenchmarkLoggerFilteredNone(b *testing.B) {
	logger := NewLogger()
	logger.SetLogLevel(LogNone)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, false)
	}
}

func BenchmarkLoggerFilteredBelowThreshold(b *testing.B) {
	logger := NewLogger()
	logger.SetLogLevel(LogCritical)

	for b.Loop() {
		logger.log(benchMsg, LogDebug, false)
	}
}

// ---------------------------------------------------------------------------
// Callback destination
// ---------------------------------------------------------------------------

func BenchmarkLoggerCallback(b *testing.B) {
	logger := NewLogger()
	logger.SetLogLevel(LogExtreme)
	logger.SetLogDest(LogCallback)
	logger.SetLogCallback(func(string) {})

	for b.Loop() {
		logger.log(benchMsg, LogNotice, false)
	}
}

// ---------------------------------------------------------------------------
// Parallel logging — /dev/null
// ---------------------------------------------------------------------------

func BenchmarkLoggerDevNullParallel(b *testing.B) {
	logger := benchLoggerDevNull(b, LogNotice, false)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.log(benchMsg, LogNotice, false)
		}
	})
}

// ---------------------------------------------------------------------------
// Parallel logging — /tmp file
// ---------------------------------------------------------------------------

func BenchmarkLoggerFileParallel(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogNotice, false)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			logger.log(benchMsg, LogNotice, false)
		}
	})
}

// ---------------------------------------------------------------------------
// Setter throughput — reconfiguration cost
// ---------------------------------------------------------------------------

func BenchmarkLoggerSetLogLevel(b *testing.B) {
	logger := NewLogger()
	levels := []int{LogCritical, LogError, LogWarning, LogNotice, LogInfo, LogVerbose, LogDebug, LogExtreme}

	for i := 0; b.Loop(); i++ {
		logger.SetLogLevel(levels[i%len(levels)])
	}
}

func BenchmarkLoggerSetLogDest(b *testing.B) {
	logger := NewLogger()
	dests := []int{LogStdout, LogDestFile, LogCallback}

	for i := 0; b.Loop(); i++ {
		logger.SetLogDest(dests[i%len(dests)])
	}
}

func BenchmarkLoggerSetCompactLogFmt(b *testing.B) {
	logger := NewLogger()

	for i := 0; b.Loop(); i++ {
		logger.SetCompactLogFmt(i%2 == 0)
	}
}

// ---------------------------------------------------------------------------
// File destination with rotation stat check — measures the os.Stat overhead
// present on every file write (since LogMaxSize is a package const we cannot
// shrink from tests; this just captures the per-write stat cost).
// ---------------------------------------------------------------------------

func BenchmarkLoggerFileWithStatCheck(b *testing.B) {
	logger, _ := benchLoggerTempFile(b, LogNotice, false)

	for b.Loop() {
		logger.log(benchMsg, LogNotice, false)
	}
}
