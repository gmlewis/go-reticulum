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

var (
	// LogLevel dictates the current operational verbosity of the logging subsystem.
	LogLevel   = LogNotice
	logLevelMu sync.RWMutex
	// LogFilePath specifies an absolute path where log output will be appended if file logging is enabled.
	LogFilePath string
	// LogDest determines where log messages are fundamentally routed, such as stdout, file, or callback.
	LogDest = LogStdout
	// LogCall holds a custom callback function triggered for every log event if the destination is set to callback.
	LogCall func(string)
	// LogTimeFmt defines the standard timestamp format used in log entries.
	LogTimeFmt = "2006-01-02 15:04:05"
	// LogTimeFmtP defines a precise timestamp format including milliseconds, typically used for performance logging.
	LogTimeFmtP = "15:04:05.000"
	// CompactLogFmt toggles a leaner log output format that removes semantic log level labels.
	CompactLogFmt = false
	// LoggingLock strictly serializes writes to the active log destination to prevent interleaved output.
	LoggingLock sync.Mutex
	// AlwaysOverride forces log messages to write to standard output regardless of the configured destination.
	AlwaysOverride = false
)

// SetLogLevel safely updates the global operational verbosity for the logging subsystem.
func SetLogLevel(level int) {
	logLevelMu.Lock()
	LogLevel = level
	logLevelMu.Unlock()
}

// GetLogLevel safely retrieves the global operational verbosity currently applied to the logging subsystem.
func GetLogLevel() int {
	logLevelMu.RLock()
	defer logLevelMu.RUnlock()
	return LogLevel
}

// Log constructs, formats, and safely writes a distinct log message to the configured system destination.
func Log(msg string, level int, pt bool) {
	currentLogLevel := GetLogLevel()
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

		if CompactLogFmt {
			logString = fmt.Sprintf("[%v] %v", timeStr, msg)
		} else {
			logString = fmt.Sprintf("[%v] %v %v", timeStr, LogLevelName(level), msg)
		}

		LoggingLock.Lock()
		defer LoggingLock.Unlock()

		if LogDest == LogStdout || AlwaysOverride {
			fmt.Println(logString)
		} else if LogDest == LogDestFile && LogFilePath != "" {
			f, err := os.OpenFile(LogFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				AlwaysOverride = true
				fmt.Printf("[%v] [Critical] Exception occurred while writing log message to log file: %v\n", timeStr, err)
				fmt.Printf("[%v] [Critical] Dumping future log events to console!\n", timeStr)
				fmt.Println(logString)
				return
			}
			defer func() {
				if closeErr := f.Close(); closeErr != nil {
					AlwaysOverride = true
					fmt.Printf("[%v] [Critical] Exception occurred while closing log file: %v\n", timeStr, closeErr)
				}
			}()

			if _, err := f.WriteString(logString + "\n"); err != nil {
				AlwaysOverride = true
				fmt.Printf("[%v] [Critical] Exception occurred while writing log message to log file: %v\n", timeStr, err)
				fmt.Printf("[%v] [Critical] Dumping future log events to console!\n", timeStr)
				fmt.Println(logString)
				return
			}

			fi, err := f.Stat()
			if err == nil && fi.Size() > LogMaxSize {
				prevFile := LogFilePath + ".1"
				if _, err := os.Stat(prevFile); err == nil {
					if rmErr := os.Remove(prevFile); rmErr != nil {
						AlwaysOverride = true
						fmt.Printf("[%v] [Critical] Exception occurred while rotating log file: %v\n", timeStr, rmErr)
					}
				}
				if renameErr := os.Rename(LogFilePath, prevFile); renameErr != nil {
					AlwaysOverride = true
					fmt.Printf("[%v] [Critical] Exception occurred while rotating log file: %v\n", timeStr, renameErr)
				}
			}
		} else if LogDest == LogCallback && LogCall != nil {
			LogCall(logString)
		}
	}
}

// Logf provides string formatting convenience over the standard logging function.
func Logf(format string, level int, pt bool, args ...any) {
	Log(fmt.Sprintf(format, args...), level, pt)
}

// TraceException formats and logs an error struct directly as a discrete, high-severity error event.
func TraceException(err error) {
	Logf("An unhandled exception occurred: %v", LogError, false, err)
}
