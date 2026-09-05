// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// LogTimeFmt defines the standard timestamp format used in log entries.
	LogTimeFmt = "2006-01-02 15:04:05"
	// LogTimeFmtP defines a precise timestamp format including milliseconds, typically used for performance logging.
	LogTimeFmtP = "15:04:05.000"
)

const (
	// LogQueueDepth bounds the async writer's pending-line queue. The packet
	// goroutines enqueue formatted lines and never wait on sink I/O; when a
	// burst exceeds the depth the overflow is dropped and counted instead of
	// stalling the transport.
	LogQueueDepth = 16384
	// logDropWarnInterval rate-limits the queue-overflow warning the writer
	// emits into the sink itself.
	logDropWarnInterval = time.Second
	// LogFlushTimeout bounds how long Flush waits for the writer to drain.
	LogFlushTimeout = 5 * time.Second
)

// logJob is one formatted log line waiting on the writer goroutine, or a
// flush marker when done is non-nil (the marker closes after every line
// submitted before it has reached the sink, giving Flush exact FIFO
// semantics).
type logJob struct {
	line string
	done chan struct{}
}

// Logger stores the configuration and sinks for Reticulum log output.
type Logger struct {
	// mu ensures that state changes are atomic
	mu sync.RWMutex

	level        int
	pendingDelta int
	dest         int
	call         func(string)

	filePath string
	compact  bool
	override bool
	// timestamps gates the "[<timestamp>] " log prefix (RNS.logtimestamps,
	// RNS/__init__.py:86). Defaults to true; when false the prefix is omitted.
	timestamps bool

	// lock serializes sink writes.
	lock sync.Mutex

	// The async writer decouples the sink's file I/O from the calling
	// goroutines: log() formats and enqueues, the single writer goroutine
	// performs the open/write/stat/rotate/close. Without it the transport's
	// packet goroutines serialize on every sink syscall and a slow disk backs
	// up packet delivery by tens of seconds (the observed raspberrypi
	// pipeline stall).
	logCh      chan logJob
	stopCh     chan struct{}
	writerOnce sync.Once
	writerWG   sync.WaitGroup
	closed     atomic.Bool
	dropped    atomic.Uint64
	dropWarned atomic.Int64
}

// NewLogger creates a logger with the default notice level and stdout output.
func NewLogger() *Logger {
	return &Logger{
		level:      LogNotice,
		dest:       LogStdout,
		timestamps: true,
	}
}

// SetAlwaysOverride forces log output to the console even when another
// destination is configured. It is latched true by the file-write failure paths
// below when file logging breaks. Because a TUI application (gonomadnet) runs
// with the terminal in raw mode + the alternate screen, routing that fallback to
// stdout floods the terminal's scrollback and balloons the emulator's memory. The
// override sink is therefore stderr, not stdout: a launcher that redirects stderr
// to a file captures the logs without touching the live terminal.
func (s *Logger) SetAlwaysOverride(override bool) {
	if s == nil {
		return // for tests
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.override = override
}

// GetAlwaysOverride reports whether console (stderr) override is currently enabled.
func (s *Logger) GetAlwaysOverride() bool {
	if s == nil {
		return false // for tests
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.override
}

// SetCompactLogFmt toggles the compact log format that omits severity labels.
func (s *Logger) SetCompactLogFmt(compact bool) {
	if s == nil {
		return // for tests
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compact = compact
}

// GetCompactLogFmt reports whether compact log formatting is enabled.
func (s *Logger) GetCompactLogFmt() bool {
	if s == nil {
		return false // for tests
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.compact
}

// SetLogTimestamps toggles the "[<timestamp>] " log prefix
// (RNS.logtimestamps, RNS/__init__.py:86 / RNS/Reticulum.py:463-465, v1.3.2).
// Defaults to true; set false to omit the prefix.
func (s *Logger) SetLogTimestamps(enabled bool) {
	if s == nil {
		return // for tests
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timestamps = enabled
}

// GetLogTimestamps reports whether the timestamp prefix is emitted.
func (s *Logger) GetLogTimestamps() bool {
	if s == nil {
		return true // for tests — default matches NewLogger
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.timestamps
}

// SetLogLevel sets the minimum severity level that will be emitted.
func (s *Logger) SetLogLevel(level int) {
	if s == nil {
		return // for tests
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingDelta != 0 {
		level += s.pendingDelta
		if level < LogCritical {
			level = LogCritical
		}
		if level > LogExtreme {
			level = LogExtreme
		}
		s.pendingDelta = 0
	}
	s.level = level
}

// GetLogLevel returns the current minimum log severity.
func (s *Logger) GetLogLevel() int {
	if s == nil {
		return 0 // for tests
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.level
}

// SetPendingDelta configures an adjustment that will be applied to the next
// SetLogLevel call.
func (s *Logger) SetPendingDelta(delta int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingDelta = delta
}

// SetLogFilePath sets the path used when the logger writes to a file sink.
func (s *Logger) SetLogFilePath(path string) {
	if s == nil {
		return // for tests
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filePath = path
}

// GetLogFilePath returns the configured log file path.
func (s *Logger) GetLogFilePath() string {
	if s == nil {
		return "" // for tests
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filePath
}

// SetLogDest selects the active log destination.
func (s *Logger) SetLogDest(dest int) {
	if s == nil {
		return // for tests
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dest = dest
}

// GetLogDest returns the active log destination.
func (s *Logger) GetLogDest() int {
	if s == nil {
		return 0 // for tests
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dest
}

// SetLogCallback registers the callback used when the log destination is
// LogCallback.
func (s *Logger) SetLogCallback(call func(string)) {
	if s == nil {
		return // for tests
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call = call
}

// GetLogCallback returns the currently configured log callback.
func (s *Logger) GetLogCallback() func(string) {
	if s == nil {
		return func(string) {} // for tests
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.call
}

// log constructs, formats, and safely submits a distinct log message to the
// configured system destination. The caller never waits on sink I/O: the
// formatted line is enqueued for the async writer goroutine.
func (s *Logger) log(msg string, level int, preciseTimestamp bool) {
	if s == nil {
		log.Printf("(nil logger): %v", msg)
		return
	}

	currentLogLevel := s.GetLogLevel()
	if currentLogLevel == LogNone {
		return
	}

	if currentLogLevel >= level {
		logString := s.formatLine(msg, level, time.Now(), preciseTimestamp)

		// The callback sink stays synchronous: it is an in-memory sink (the
		// TUI bridge), delivery ordering is caller-observed, and callers may
		// read their capture buffers immediately after logging. The slow I/O
		// sinks (file, stdout) are what the async writer exists for.
		if !s.closed.Load() && s.GetLogDest() == LogCallback {
			if callback := s.GetLogCallback(); callback != nil {
				callback(logString)
			}
			return
		}

		if s.closed.Load() {
			// The writer is stopped: fall back to the standard logger so
			// late messages neither panic on a stopped queue nor block.
			log.Print(logString)
			return
		}
		s.writerOnce.Do(s.startWriter)
		select {
		case s.logCh <- logJob{line: logString}:
		default:
			s.dropped.Add(1)
		}
	}
}

// formatLine renders the final log line (timestamp prefix, level label,
// message) honoring the compact-format and timestamp settings.
func (s *Logger) formatLine(msg string, level int, now time.Time, preciseTimestamp bool) string {
	timeStr := ""
	if s.GetLogTimestamps() {
		if preciseTimestamp {
			timeStr = now.Format(LogTimeFmtP)
		} else {
			timeStr = now.Format(LogTimeFmt)
		}
	}

	if s.GetCompactLogFmt() {
		if timeStr != "" {
			return fmt.Sprintf("[%v] %v", timeStr, msg)
		}
		return msg
	}
	if timeStr != "" {
		return fmt.Sprintf("[%v] %v %v", timeStr, LogLevelName(level), msg)
	}
	return fmt.Sprintf("%v %v", LogLevelName(level), msg)
}

// startWriter spawns the single async sink writer. It runs exactly once per
// logger, on the first log call or Flush/Close, whichever comes first.
func (s *Logger) startWriter() {
	s.logCh = make(chan logJob, LogQueueDepth)
	s.stopCh = make(chan struct{})
	s.writerWG.Add(1)
	go s.writerLoop()
}

// writerLoop drains the queue into the sink. On stop it first writes every
// already-queued line, then exits.
func (s *Logger) writerLoop() {
	defer s.writerWG.Done()
	for {
		select {
		case job := <-s.logCh:
			if job.done != nil {
				close(job.done)
				continue
			}
			s.writeSink(job.line)
			s.warnDropped()
		case <-s.stopCh:
			for {
				select {
				case job := <-s.logCh:
					if job.done != nil {
						close(job.done)
						continue
					}
					s.writeSink(job.line)
				default:
					return
				}
			}
		}
	}
}

// writeSink delivers one formatted line to the configured destination. It
// runs only on the writer goroutine (or a post-close fallback), so the file
// open/write/stat/rotate/close sequence no longer sits on the transport's
// packet path. The rotation and latching behavior matches the original
// synchronous logger.
func (s *Logger) writeSink(logString string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	// critTS is always populated for the internal critical-error fallback
	// diagnostics below (file write/close/rotate failures), independent of
	// the user's logtimestamps setting, so those emergency messages stay
	// diagnosable even when timestamp prefixes are disabled.
	critTS := time.Now().Format(LogTimeFmt)

	dest := s.GetLogDest()
	filePath := s.GetLogFilePath()

	if dest == LogStdout {
		// Explicitly configured for the console (e.g. daemon -console mode
		// with no TUI). Honor stdout as the user requested.
		fmt.Println(logString)
	} else if s.GetAlwaysOverride() {
		// File logging was requested but a prior open/write/close/rotate
		// failed and latched the override. Route to stderr — NOT stdout —
		// so a TUI app running in raw mode + the alternate screen never has
		// its terminal scrollback flooded (which balloons emulator memory).
		// A launcher that redirects stderr to a file still captures these.
		log.Print(logString)
	} else if dest == LogDestFile && filePath != "" {
		f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			s.SetAlwaysOverride(true)
			log.Printf("[%v] [Critical] Exception occurred while writing log message to log file: %v\n", critTS, err)
			log.Printf("[%v] [Critical] Dumping future log events to console!\n", critTS)
			log.Print(logString)
			return
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				s.SetAlwaysOverride(true)
				log.Printf("[%v] [Critical] Exception occurred while closing log file: %v\n", critTS, closeErr)
			}
		}()

		if _, err := f.WriteString(logString + "\n"); err != nil {
			s.SetAlwaysOverride(true)
			log.Printf("[%v] [Critical] Exception occurred while writing log message to log file: %v\n", critTS, err)
			log.Printf("[%v] [Critical] Dumping future log events to console!\n", critTS)
			log.Print(logString)
			return
		}

		fi, err := f.Stat()
		if err == nil && fi.Size() > LogMaxSize {
			prevFile := filePath + ".1"
			if _, err := os.Stat(prevFile); err == nil {
				if rmErr := os.Remove(prevFile); rmErr != nil {
					s.SetAlwaysOverride(true)
					log.Printf("[%v] [Critical] Exception occurred while rotating log file: %v\n", critTS, rmErr)
				}
			}
			if renameErr := os.Rename(filePath, prevFile); renameErr != nil {
				s.SetAlwaysOverride(true)
				log.Printf("[%v] [Critical] Exception occurred while rotating log file: %v\n", critTS, renameErr)
			}
		}
	} else if dest == LogCallback {
		if callback := s.GetLogCallback(); callback != nil {
			callback(logString)
		}
	}
}

// warnDropped emits a sink-level warning when queue overflow dropped lines.
// It runs on the writer goroutine and writes directly (bypassing the queue)
// so the warning cannot be dropped by the condition it reports.
func (s *Logger) warnDropped() {
	dropped := s.dropped.Load()
	if dropped == 0 {
		return
	}
	now := time.Now().UnixNano()
	last := s.dropWarned.Load()
	if now-last < int64(logDropWarnInterval) {
		return
	}
	if !s.dropWarned.CompareAndSwap(last, now) {
		return
	}
	s.writeSink(s.formatLine(fmt.Sprintf("%v log messages dropped because the log queue was full", dropped), LogWarning, time.Now(), false))
}

// Flush waits — bounded by LogFlushTimeout — for every line submitted before
// the call to reach the sink. It returns false when the queue is full or the
// writer is stuck on sink I/O, so callers can proceed without blocking.
func (s *Logger) Flush() bool {
	if s == nil {
		return true
	}
	if s.closed.Load() {
		return true
	}
	s.writerOnce.Do(s.startWriter)
	done := make(chan struct{})
	select {
	case s.logCh <- logJob{done: done}:
	default:
		return false
	}
	select {
	case <-done:
		return true
	case <-time.After(LogFlushTimeout):
		return false
	}
}

// Close stops the async writer after draining every queued line. Later log
// calls fall back to the standard logger instead of blocking. Close blocks
// until the writer exits; if the sink is wedged on I/O the caller is expected
// to have already given up on those lines. Idempotent.
func (s *Logger) Close() {
	if s == nil {
		return
	}
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	s.writerOnce.Do(s.startWriter)
	close(s.stopCh)
	s.writerWG.Wait()
}

// DroppedCount reports how many log lines have been dropped because the
// async writer's queue was full.
func (s *Logger) DroppedCount() uint64 {
	if s == nil {
		return 0
	}
	return s.dropped.Load()
}

// Critical logs a critical message.
func (s *Logger) Critical(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log(msg, LogCritical, false)
}

// Error logs an error message.
func (s *Logger) Error(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log(msg, LogError, false)
}

// Warning logs a warning message.
func (s *Logger) Warning(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log(msg, LogWarning, false)
}

// Notice logs a notice message.
func (s *Logger) Notice(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log(msg, LogNotice, false)
}

// Info logs an info message.
func (s *Logger) Info(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log(msg, LogInfo, false)
}

// Verbose logs a verbose message.
func (s *Logger) Verbose(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log(msg, LogVerbose, false)
}

// Debug logs a debug message.
func (s *Logger) Debug(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log(msg, LogDebug, false)
}

// Pathing logs a path-finding/routing detail message (RNS LOG_PATHING, v1.3.9).
func (s *Logger) Pathing(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log(msg, LogPathing, false)
}

// Extreme logs an extreme message.
func (s *Logger) Extreme(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.log(msg, LogExtreme, false)
}

const (
	// LogStdout configures the logging subsystem to write to standard output.
	LogStdout = 0x91
	// LogDestFile configures the logging subsystem to append output to a specific file on disk.
	LogDestFile = 0x92
	// LogCallback configures the logging subsystem to route messages to a custom callback function.
	LogCallback = 0x93
)

const (
	// LogMaxSize defines the maximum file size (in bytes) before a log rotation is triggered.
	LogMaxSize = 5 * 1024 * 1024
)

const (
	// LogNone completely disables the output of the logging subsystem.
	LogNone = -1
	// LogCritical designates the most severe level of failure, requiring immediate attention.
	LogCritical = 0
	// LogError designates an error state that interrupts a specific operation but not the entire system.
	LogError = 1
	// LogWarning designates a potential issue or unexpected condition that does not halt the system.
	LogWarning = 2
	// LogNotice designates a significant event that is not an error.
	LogNotice = 3
	// LogInfo designates informational progress about routine operations.
	LogInfo = 4
	// LogVerbose designates detailed information primarily useful for tracing operations.
	LogVerbose = 5
	// LogDebug designates low-level system details for in-depth troubleshooting.
	LogDebug = 6
	// LogPathing designates path-finding/routing detail logging
	// (RNS/__init__.py LOG_PATHING, v1.3.9).
	LogPathing = 7
	// LogExtreme designates an exhaustive level of logging, outputting almost all internal events.
	LogExtreme = 8
)

// LogLevelName maps an integer logging level back to its human-readable console tag representation.
func LogLevelName(level int) string {
	switch level {
	case LogCritical:
		return "[Critical]"
	case LogError:
		return "[Error]   "
	case LogWarning:
		return "[Warning] "
	case LogNotice:
		return "[Notice]  "
	case LogInfo:
		return "[Info]    "
	case LogVerbose:
		return "[Verbose] "
	case LogDebug:
		return "[Debug]   "
	case LogPathing:
		return "[Pathing] "
	case LogExtreme:
		return "[Extra]   "
	default:
		return "[Unknown] "
	}
}
