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
	LogLevel       = LogNotice
	logLevelMu     sync.RWMutex
	LogFilePath    string
	LogDest        = LogStdout
	LogCall        func(string)
	LogTimeFmt     = "2006-01-02 15:04:05"
	LogTimeFmtP    = "15:04:05.000"
	CompactLogFmt  = false
	LoggingLock    sync.Mutex
	AlwaysOverride = false
)

func SetLogLevel(level int) {
	logLevelMu.Lock()
	LogLevel = level
	logLevelMu.Unlock()
}

func GetLogLevel() int {
	logLevelMu.RLock()
	defer logLevelMu.RUnlock()
	return LogLevel
}

// Log formats and writes a log message.
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

// Logf formats and writes a log message using a format string.
func Logf(format string, level int, pt bool, args ...any) {
	Log(fmt.Sprintf(format, args...), level, pt)
}

// TraceException logs an error and its context.
func TraceException(err error) {
	Logf("An unhandled exception occurred: %v", LogError, false, err)
}
