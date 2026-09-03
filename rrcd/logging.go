// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/gmlewis/go-reticulum/rns"
)

// LogSetup describes the configured logging behavior of the hub, mapped
// from HubConfig's log_* fields the way Python's configure_logging does.
//
// Level mapping onto this repository's stack: the hub's own messages go
// through Go's log package filtered at the parsed Python-style level
// (Python DEBUG=10 … CRITICAL=50, NOTSET=0; see ParseLogLevel), and the RNS
// logger level comes from log_rns_level via the rns package constants
// (WARNING → rns.LogWarning, INFO → rns.LogInfo, DEBUG → rns.LogDebug,
// ERROR → rns.LogError, CRITICAL → rns.LogCritical, NOTSET → rns.LogNone).
type LogSetup struct {
	// Level is the parsed hub log level.
	Level slog.Level
	// RNSLevel is the parsed RNS logger level.
	RNSLevel int
	// Console enables the stderr handler.
	Console bool
	// File is the optional log file path (nil when disabled).
	File *string
	// Format is the effective format string; a blank configured format
	// falls back to the no-threadName default.
	Format string
	// Writer receives the hub's own log output (stderr and/or the file).
	Writer io.Writer
}

// DefaultLogFormat is the fallback format when the configured format is
// blank (no threadName).
const DefaultLogFormat = "%(asctime)s %(levelname)s %(name)s: %(message)s"

// ParseLogLevel parses a Python logging level name or numeric string the
// way logging_config._parse_level does, returning def for unknown values.
func ParseLogLevel(value any, def slog.Level) slog.Level {
	if value == nil {
		return def
	}
	switch n := value.(type) {
	case int:
		return slogLevelFromNumber(n)
	case int64:
		return slogLevelFromNumber(int(n))
	case float64:
		return slogLevelFromNumber(int(n))
	}
	text := strings.ToUpper(strings.TrimSpace(fmtValue(value)))
	if text == "" {
		return def
	}
	switch text {
	case "CRITICAL":
		return slogLevelCritical
	case "ERROR":
		return slog.LevelError
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "INFO":
		return slog.LevelInfo
	case "DEBUG":
		return slog.LevelDebug
	case "NOTSET":
		return slogLevelNotSet
	}
	if n, err := strconv.Atoi(text); err == nil {
		return slogLevelFromNumber(n)
	}
	return def
}

// Numeric slog levels mirroring the Python logging scale.
const (
	slogLevelNotSet   slog.Level = slog.LevelDebug - 10
	slogLevelCritical slog.Level = slog.LevelError + 4
)

// slogLevelFromNumber converts a Python numeric logging level to a slog
// level while keeping the original spacing.
func slogLevelFromNumber(n int) slog.Level {
	switch {
	case n <= 0:
		return slogLevelNotSet
	case n < 20:
		return slog.LevelDebug
	case n < 30:
		return slog.LevelInfo
	case n < 40:
		return slog.LevelWarn
	case n < 50:
		return slog.LevelError
	default:
		return slogLevelCritical
	}
}

// ParseRNSLevel parses a Python logging level name into an rns logger level.
func ParseRNSLevel(value any, def int) int {
	if value == nil {
		return def
	}
	switch n := value.(type) {
	case int:
		return mapPythonToRNS(n, def)
	case int64:
		return mapPythonToRNS(int(n), def)
	}
	text := strings.ToUpper(strings.TrimSpace(fmtValue(value)))
	if text == "" {
		return def
	}
	switch text {
	case "CRITICAL":
		return rns.LogCritical
	case "ERROR":
		return rns.LogError
	case "WARN", "WARNING":
		return rns.LogWarning
	case "INFO":
		return rns.LogInfo
	case "DEBUG":
		return rns.LogDebug
	case "NOTSET":
		return rns.LogNone
	}
	if n, err := strconv.Atoi(text); err == nil {
		return mapPythonToRNS(n, def)
	}
	return def
}

// mapPythonToRNS maps a Python numeric level to an rns logger level.
func mapPythonToRNS(n, def int) int {
	switch {
	case n <= 0:
		return rns.LogNone
	case n < 20:
		return rns.LogDebug
	case n < 30:
		return rns.LogInfo
	case n < 40:
		return rns.LogWarning
	case n < 50:
		return rns.LogError
	default:
		return rns.LogCritical
	}
}

// fmtValue renders an arbitrary config value as text.
func fmtValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// ConfigureLogging maps the log_* config fields onto the process the way
// Python's configure_logging does: console output goes to stderr, the
// optional log file opens in append mode with best-effort 0o600
// permissions, and the format fallback drops threadName. Reconfiguration on
// /reload replaces the previous writers.
func ConfigureLogging(cfg HubConfig, rnsLogger *rns.Logger, overrideLevel, overrideFile *string) *LogSetup {
	setup := &LogSetup{}

	levelValue := any(nil)
	if overrideLevel != nil {
		levelValue = *overrideLevel
	}
	setup.Level = ParseLogLevel(levelValue, slog.LevelInfo)
	if levelValue == nil {
		setup.Level = ParseLogLevel(cfg.LogLevel, slog.LevelInfo)
	}
	setup.RNSLevel = ParseRNSLevel(cfg.LogRNSLevel, rns.LogWarning)

	if cfg.LogConsole {
		setup.Writer = os.Stderr
	}
	filePath := overrideFile
	if filePath == nil {
		filePath = cleanOptionalPathPtr(cfg.LogFile)
	}
	if filePath != nil {
		p := ExpandPath(*filePath)
		if dir := parentDir(p); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_ = os.Chmod(p, 0o600)
			if setup.Writer != nil {
				setup.Writer = io.MultiWriter(os.Stderr, f)
			} else {
				setup.Writer = f
			}
			setup.File = filePath
		}
	}
	setup.Console = cfg.LogConsole

	format := strings.TrimSpace(cfg.LogFormat)
	if format == "" {
		format = DefaultLogFormat
	}
	setup.Format = format

	if setup.Writer != nil {
		log.SetOutput(setup.Writer)
	} else {
		log.SetOutput(io.Discard)
	}
	log.SetFlags(0)

	if rnsLogger != nil {
		rnsLogger.SetLogLevel(setup.RNSLevel)
	}
	return setup
}

// cleanOptionalPathPtr mirrors _clean_optional_path: whitespace-only paths
// are nil.
func cleanOptionalPathPtr(v *string) *string {
	if v == nil {
		return nil
	}
	if strings.TrimSpace(*v) == "" {
		return nil
	}
	return v
}

// parentDir returns the directory part of a path.
func parentDir(p string) string {
	idx := strings.LastIndexByte(p, '/')
	if idx <= 0 {
		return ""
	}
	return p[:idx]
}
