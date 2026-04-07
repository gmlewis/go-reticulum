// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import (
	"fmt"
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
	mu sync.RWMutex

	level int
	dest  int
	call  func(string)

	filePath string
	compact  bool
	override bool

	lock sync.Mutex
}

// NewLogger creates a logger with the default notice level and stdout output.
func NewLogger() *Logger {
	return &Logger{
		level: LogNotice,
		dest:  LogStdout,
	}
}

var logger = NewLogger()

// SetAlwaysOverride safely updates the AlwaysOverride setting.
func SetAlwaysOverride(override bool) {
	logger.SetAlwaysOverride(override)
}

// GetAlwaysOverride safely retrieves the AlwaysOverride setting.
func GetAlwaysOverride() bool {
	return logger.GetAlwaysOverride()
}

// SetCompactLogFmt safely updates the CompactLogFmt setting.
func SetCompactLogFmt(compact bool) {
	logger.SetCompactLogFmt(compact)
}

// GetCompactLogFmt safely retrieves the CompactLogFmt setting.
func GetCompactLogFmt() bool {
	return logger.GetCompactLogFmt()
}

// SetLogLevel safely updates the global operational verbosity for the logging subsystem.
func SetLogLevel(level int) {
	logger.SetLogLevel(level)
}

// GetLogLevel safely retrieves the global operational verbosity currently applied to the logging subsystem.
func GetLogLevel() int {
	return logger.GetLogLevel()
}

// SetLogFilePath safely sets the path to the log file.
func SetLogFilePath(path string) {
	logger.SetLogFilePath(path)
}

// GetLogFilePath safely retrieves the current log file path.
func GetLogFilePath() string {
	return logger.GetLogFilePath()
}

// SetLogDest safely sets the log destination.
func SetLogDest(dest int) {
	logger.SetLogDest(dest)
}

// GetLogDest safely retrieves the current log destination.
func GetLogDest() int {
	return logger.GetLogDest()
}

// SetLogCallback safely sets the log callback function.
func SetLogCallback(call func(string)) {
	logger.SetLogCallback(call)
}

// GetLogCallback safely retrieves the current log callback function.
func GetLogCallback() func(string) {
	return logger.GetLogCallback()
}

func (s *Logger) SetAlwaysOverride(override bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.override = override
}

func (s *Logger) GetAlwaysOverride() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.override
}

func (s *Logger) SetCompactLogFmt(compact bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compact = compact
}

func (s *Logger) GetCompactLogFmt() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.compact
}

func (s *Logger) SetLogLevel(level int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.level = level
}

func (s *Logger) GetLogLevel() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.level
}

func (s *Logger) SetLogFilePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filePath = path
}

func (s *Logger) GetLogFilePath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filePath
}

func (s *Logger) SetLogDest(dest int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dest = dest
}

func (s *Logger) GetLogDest() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dest
}

func (s *Logger) SetLogCallback(call func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call = call
}

func (s *Logger) GetLogCallback() func(string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.call
}

// Log constructs, formats, and safely writes a distinct log message to the configured system destination.
func (s *Logger) Log(msg string, level int, pt bool) {
	currentLogLevel := s.GetLogLevel()
	if currentLogLevel == LogNone {
		return
	}

	if currentLogLevel >= level {
		var logString string
		now := time.Now()

		timeStr := ""
		if pt {
			timeStr = now.Format(LogTimeFmtP)
		} else {
			timeStr = now.Format(LogTimeFmt)
		}

		if s.GetCompactLogFmt() {
			logString = fmt.Sprintf("[%v] %v", timeStr, msg)
		} else {
			logString = fmt.Sprintf("[%v] %v %v", timeStr, LogLevelName(level), msg)
		}

		s.lock.Lock()
		defer s.lock.Unlock()

		dest := s.GetLogDest()
		filePath := s.GetLogFilePath()

		if dest == LogStdout || s.GetAlwaysOverride() {
			fmt.Println(logString)
		} else if dest == LogDestFile && filePath != "" {
			f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				s.SetAlwaysOverride(true)
				fmt.Printf("[%v] [Critical] Exception occurred while writing log message to log file: %v\n", timeStr, err)
				fmt.Printf("[%v] [Critical] Dumping future log events to console!\n", timeStr)
				fmt.Println(logString)
				return
			}
			defer func() {
				if closeErr := f.Close(); closeErr != nil {
					s.SetAlwaysOverride(true)
					fmt.Printf("[%v] [Critical] Exception occurred while closing log file: %v\n", timeStr, closeErr)
				}
			}()

			if _, err := f.WriteString(logString + "\n"); err != nil {
				s.SetAlwaysOverride(true)
				fmt.Printf("[%v] [Critical] Exception occurred while writing log message to log file: %v\n", timeStr, err)
				fmt.Printf("[%v] [Critical] Dumping future log events to console!\n", timeStr)
				fmt.Println(logString)
				return
			}

			fi, err := f.Stat()
			if err == nil && fi.Size() > LogMaxSize {
				prevFile := filePath + ".1"
				if _, err := os.Stat(prevFile); err == nil {
					if rmErr := os.Remove(prevFile); rmErr != nil {
						s.SetAlwaysOverride(true)
						fmt.Printf("[%v] [Critical] Exception occurred while rotating log file: %v\n", timeStr, rmErr)
					}
				}
				if renameErr := os.Rename(filePath, prevFile); renameErr != nil {
					s.SetAlwaysOverride(true)
					fmt.Printf("[%v] [Critical] Exception occurred while rotating log file: %v\n", timeStr, renameErr)
				}
			}
		} else if dest == LogCallback {
			if callback := s.GetLogCallback(); callback != nil {
				callback(logString)
			}
		}
	}
}

// Log writes a message using the package-level logger.
func Log(msg string, level int, pt bool) {
	logger.Log(msg, level, pt)
}

// Logf formats and writes a message using the package-level logger.
func Logf(format string, level int, pt bool, args ...any) {
	Log(fmt.Sprintf(format, args...), level, pt)
}

// TraceException formats and logs an error struct directly as a discrete, high-severity error event.
func TraceException(err error) {
	Logf("An unhandled exception occurred: %v", LogError, false, err)
}
