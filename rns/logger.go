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
	"time"
)

const (
	// LogTimeFmt defines the standard timestamp format used in log entries.
	LogTimeFmt = "2006-01-02 15:04:05"
	// LogTimeFmtP defines a precise timestamp format including milliseconds, typically used for performance logging.
	LogTimeFmtP = "15:04:05.000"
)

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

	// lock ensures that logging output does not corrupt itself
	lock sync.Mutex
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

// log constructs, formats, and safely writes a distinct log message to the configured system destination.
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
		var logString string
		now := time.Now()

		// Timestamp prefix is gated by the logtimestamps setting
		// (RNS.logtimestamps, RNS/__init__.py:86,133, v1.3.2).
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
				logString = fmt.Sprintf("[%v] %v", timeStr, msg)
			} else {
				logString = msg
			}
		} else {
			if timeStr != "" {
				logString = fmt.Sprintf("[%v] %v %v", timeStr, LogLevelName(level), msg)
			} else {
				logString = fmt.Sprintf("%v %v", LogLevelName(level), msg)
			}
		}

		// critTS is always populated for the internal critical-error fallback
		// diagnostics below (file write/close/rotate failures), independent of
		// the user's logtimestamps setting, so those emergency messages stay
		// diagnosable even when timestamp prefixes are disabled.
		critTS := now.Format(LogTimeFmt)

		s.lock.Lock()
		defer s.lock.Unlock()

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
