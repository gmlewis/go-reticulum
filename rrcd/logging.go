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
	"sync"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
)

// LogSetup is the hub's live logging configuration: the level filter,
// format, and writer that Python keeps on its global root logger. The
// owning HubService holds one instance and Apply re-renders it in place
// across /reload swaps, so every emitter shares the same receiver and no
// package-level mutable state is needed.
//
// Level mapping onto this repository's stack: the hub's own messages go
// through Emit filtered at the parsed Python-style level (Python
// DEBUG=10 … CRITICAL=50, NOTSET=0; see ParseLogLevel), and the RNS
// logger level comes from log_rns_level via the rns package constants
// (WARNING → rns.LogWarning, INFO → rns.LogInfo, DEBUG → rns.LogDebug,
// ERROR → rns.LogError, CRITICAL → rns.LogCritical, NOTSET → rns.LogNone).
type LogSetup struct {
	// Level is the applied hub log level filter.
	Level slog.Level
	// RNSLevel is the applied RNS logger level.
	RNSLevel int
	// Console reports whether the stderr console handler is on.
	Console bool
	// File is the applied log file path (nil when disabled).
	File *string
	// Format is the effective format string; a blank configured format
	// falls back to the no-threadName default.
	Format string

	// mu guards the live fields: Emit holds it while reading and
	// writing, and Apply swaps every field under it so records in
	// flight on RNS goroutines never see a torn state.
	mu     sync.Mutex
	writer io.Writer
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

// hubLogThreadName stands in for Python's %(threadName)s: Go goroutines
// carry no names, so the field always renders as MainThread — a
// documented mechanical divergence.
const hubLogThreadName = "MainThread"

// ConfigureLogging renders a fresh LogSetup from the config fields the
// way Python's configure_logging does. The caller that owns the returned
// instance re-runs Apply on it for reconfiguration; the HubService
// creates its own through NewLogSetup.
func ConfigureLogging(cfg HubConfig, rnsLogger *rns.Logger, overrideLevel, overrideFile *string) *LogSetup {
	setup := NewLogSetup()
	setup.Apply(cfg, rnsLogger, overrideLevel, overrideFile)
	return setup
}

// NewLogSetup returns the default hub logging state: INFO level with the
// fallback format and no writer, so records are dropped until the owner
// applies a configuration.
func NewLogSetup() *LogSetup {
	return &LogSetup{
		Level:  slog.LevelInfo,
		Format: DefaultLogFormat,
	}
}

// Apply re-renders the setup in place from the log_* config fields the
// way Python's configure_logging re-runs: console output goes to stderr,
// the optional log file opens in append mode with best-effort 0o600
// permissions, the format fallback drops threadName, the parsed level
// filters every later Emit, and the RNS logger level applies to the
// passed logger. Every field swaps under the lock so records in flight
// never see a torn state.
func (s *LogSetup) Apply(cfg HubConfig, rnsLogger *rns.Logger, overrideLevel, overrideFile *string) {
	levelValue := any(nil)
	if overrideLevel != nil {
		levelValue = *overrideLevel
	}
	level := ParseLogLevel(levelValue, slog.LevelInfo)
	if levelValue == nil {
		level = ParseLogLevel(cfg.LogLevel, slog.LevelInfo)
	}
	rnsLevel := ParseRNSLevel(cfg.LogRNSLevel, rns.LogWarning)

	writer := s.consoleWriter(cfg)
	filePath := overrideFile
	if filePath == nil {
		filePath = cleanOptionalPathPtr(cfg.LogFile)
	}
	var file *string
	if filePath != nil {
		p := ExpandPath(*filePath)
		if dir := parentDir(p); dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			_ = os.Chmod(p, 0o600)
			if writer != nil {
				writer = io.MultiWriter(os.Stderr, f)
			} else {
				writer = f
			}
			file = filePath
		}
	}

	format := strings.TrimSpace(cfg.LogFormat)
	if format == "" {
		format = DefaultLogFormat
	}

	s.mu.Lock()
	s.Level = level
	s.RNSLevel = rnsLevel
	s.Console = cfg.LogConsole
	s.File = file
	s.Format = format
	s.writer = writer
	s.mu.Unlock()

	if writer != nil {
		log.SetOutput(writer)
	} else {
		log.SetOutput(io.Discard)
	}
	log.SetFlags(0)

	if rnsLogger != nil {
		rnsLogger.SetLogLevel(rnsLevel)
	}
}

// consoleWriter returns the stderr writer for the console setting.
func (s *LogSetup) consoleWriter(cfg HubConfig) io.Writer {
	if cfg.LogConsole {
		return os.Stderr
	}
	return nil
}

// Emit filters one hub log record by level and writes it through the
// configured format and writer, the way Python's root logging handlers
// do. name mirrors the Python logger name (rrcd.hub, rrcd.router, ...).
func (s *LogSetup) Emit(level slog.Level, name, format string, args ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if level < s.Level {
		return
	}
	if s.writer == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	asctime := time.Now().Format("2006-01-02 15:04:05,000")
	rendered := renderLogFormat(s.Format, asctime,
		slogLevelName(level), name, hubLogThreadName, message)
	if !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}
	_, _ = io.WriteString(s.writer, rendered)
}

// DebugEnabled reports whether debug-level hub messages pass the filter,
// mirroring Python's log.isEnabledFor(logging.DEBUG).
func (s *LogSetup) DebugEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Level <= slog.LevelDebug
}

// Writer returns the current writer, or nil when output is discarded.
func (s *LogSetup) Writer() io.Writer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writer
}

// EmitSendFailure logs one send failure in Python's two tiers:
// OSError-class failures warn with the error text, everything else logs
// at the debug tier without it.
func (s *LogSetup) EmitSendFailure(err error, linkID string, size int) {
	if sendErrorIsOSError(err) {
		s.Emit(slog.LevelWarn, "rrcd.hub", "Send failed link_id=%v bytes=%v err=%v", linkID, size, err)
		return
	}
	s.Emit(slog.LevelDebug, "rrcd.hub", "Send failed link_id=%v bytes=%v", linkID, size)
}

// slogLevelName renders a slog level as its Python logging name.
func slogLevelName(level slog.Level) string {
	switch {
	case level <= slogLevelNotSet:
		return "NOTSET"
	case level <= slog.LevelDebug:
		return "DEBUG"
	case level <= slog.LevelInfo:
		return "INFO"
	case level <= slog.LevelWarn:
		return "WARNING"
	case level <= slog.LevelError:
		return "ERROR"
	default:
		return "CRITICAL"
	}
}

// renderLogFormat substitutes the Python logging format fields the rrcd
// formats use: asctime, levelname, name, threadName, and message.
func renderLogFormat(format, asctime, levelname, name, threadName, message string) string {
	out := format
	for _, field := range [...]struct{ key, val string }{
		{"%(asctime)s", asctime},
		{"%(levelname)s", levelname},
		{"%(name)s", name},
		{"%(threadName)s", threadName},
		{"%(message)s", message},
	} {
		out = strings.ReplaceAll(out, field.key, field.val)
	}
	return out
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
