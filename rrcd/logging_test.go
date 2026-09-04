// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rrcd

import (
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestParseLogLevel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   any
		def  slog.Level
		want slog.Level
	}{
		{nil, slog.LevelInfo, slog.LevelInfo},
		{"CRITICAL", slog.LevelInfo, slogLevelCritical},
		{"ERROR", slog.LevelInfo, slog.LevelError},
		{"WARN", slog.LevelInfo, slog.LevelWarn},
		{"WARNING", slog.LevelInfo, slog.LevelWarn},
		{"INFO", slog.LevelInfo, slog.LevelInfo},
		{"DEBUG", slog.LevelInfo, slog.LevelDebug},
		{"NOTSET", slog.LevelInfo, slogLevelNotSet},
		{" info ", slog.LevelWarn, slog.LevelInfo},
		{"bogus", slog.LevelWarn, slog.LevelWarn},
		{"", slog.LevelWarn, slog.LevelWarn},
		{"30", slog.LevelInfo, slog.LevelWarn},
		{int64(20), slog.LevelWarn, slog.LevelInfo},
		{float64(40), slog.LevelWarn, slog.LevelError},
	} {
		if got := ParseLogLevel(tc.in, tc.def); got != tc.want {
			t.Errorf("ParseLogLevel(%#v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseRNSLevel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   any
		want int
	}{
		{"CRITICAL", rns.LogCritical},
		{"ERROR", rns.LogError},
		{"WARNING", rns.LogWarning},
		{"WARN", rns.LogWarning},
		{"INFO", rns.LogInfo},
		{"DEBUG", rns.LogDebug},
		{"NOTSET", rns.LogNone},
		{"bogus", rns.LogWarning},
		{int64(20), rns.LogInfo},
	} {
		def := rns.LogWarning
		if got := ParseRNSLevel(tc.in, def); got != tc.want {
			t.Errorf("ParseRNSLevel(%#v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestConfigureLoggingFileHandler(t *testing.T) {
	dir := testutils.TempDir(t, "rrcd-log-")
	logPath := filepath.Join(dir, "nested", "hub.log")
	cfg := DefaultHubConfig()
	cfg.LogConsole = false
	cfg.LogFile = new(logPath)

	setup := ConfigureLogging(cfg, rns.NewLogger(), nil, nil)
	if setup.File == nil || *setup.File != logPath {
		t.Fatalf("setup.File = %v", setup.File)
	}
	if setup.Writer() == nil {
		t.Fatal("writer missing with a file handler")
	}
	// The file must exist with 0o600 best-effort permissions.
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("log file missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("log file mode = %v, want 0600", perm)
	}
	if parent := filepath.Dir(logPath); parent != filepath.Join(dir, "nested") {
		t.Fatalf("parent dir = %v, want %v", parent, filepath.Join(dir, "nested"))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("parent dir missing: %v", err)
	}
}

func TestConfigureLoggingBlankFormatFallsBack(t *testing.T) {
	cfg := DefaultHubConfig()
	cfg.LogFormat = ""
	setup := ConfigureLogging(cfg, nil, nil, nil)
	if setup.Format != DefaultLogFormat {
		t.Errorf("blank format fallback = %q, want %q", setup.Format, DefaultLogFormat)
	}
}

func TestConfigureLoggingConsoleIsStderr(t *testing.T) {
	cfg := DefaultHubConfig()
	cfg.LogConsole = true
	cfg.LogFile = nil
	setup := ConfigureLogging(cfg, nil, nil, nil)
	if setup.Writer() != io.Writer(os.Stderr) {
		t.Fatalf("console-only writer = %T, want *os.File(stderr)", setup.Writer())
	}
}

func TestConfigureLoggingNoWritersDiscards(t *testing.T) {
	cfg := DefaultHubConfig()
	cfg.LogConsole = false
	cfg.LogFile = nil
	setup := ConfigureLogging(cfg, nil, nil, nil)
	if setup.Writer() != nil {
		t.Fatalf("writer = %v, want nil", setup.Writer())
	}
	// With no writers, the hub's own log output goes to io.Discard.
	if w := log.Writer(); w != io.Discard {
		t.Fatalf("log.Writer() = %T, want io.Discard", w)
	}
	defer log.SetOutput(io.Discard)
}

// G16.17 ConfigureLogging must APPLY the parsed level and format: an
// info-level hub message is dropped at the ERROR level while an error
// message renders through the configured format into the writer.
func TestConfigureLoggingAppliesLevelAndFormat(t *testing.T) {
	// Each LogSetup carries its own state, so no global reset is needed.

	dir := testutils.TempDir(t, "logging-")
	logPath := dir + "/hub.log"
	file := logPath
	cfg := DefaultHubConfig()
	cfg.LogLevel = "ERROR"
	cfg.LogConsole = false
	cfg.LogFile = &file
	cfg.LogFormat = "%(levelname)s:%(name)s:%(message)s"

	setup := ConfigureLogging(cfg, nil, nil, nil)

	if setup.Level != slog.LevelError || setup.Format != "%(levelname)s:%(name)s:%(message)s" {
		t.Fatalf("setup = level %v format %q", setup.Level, setup.Format)
	}

	// An info message is filtered out; the error renders per the format.
	setup.Emit(slog.LevelInfo, "rrcd.hub", "should not appear")
	setup.Emit(slog.LevelError, "rrcd.hub", "boom %v", 42)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("log file read: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "should not appear") {
		t.Errorf("the info message was not filtered at the ERROR level: %q", text)
	}
	if !strings.Contains(text, "ERROR:rrcd.hub:boom 42") {
		t.Errorf("the error message did not render per the format: %q", text)
	}

	// The default format substitutes asctime, levelname, name,
	// threadName, and message fields.
	rendered := renderLogFormat(DefaultLogFormat, "2026-01-02 03:04:05,006",
		"WARNING", "rrcd.rooms", "MainThread", "watch out")
	want := "2026-01-02 03:04:05,006 WARNING rrcd.rooms: watch out"
	if rendered != want {
		t.Errorf("default format render = %q, want %q", rendered, want)
	}
}

// G16.17 The [logging] rns_level must reach the live RNS logger instance
// the service runs with, not a discarded one.
func TestRNSLoggerLevelWired(t *testing.T) {
	// One setup re-applies its RNS level in place, so the same logger
	// instance the hub runs with always carries the configured level.

	logger := rns.NewLogger()
	cfg := DefaultHubConfig()
	cfg.LogRNSLevel = "DEBUG"
	setup := ConfigureLogging(cfg, logger, nil, nil)

	if got := logger.GetLogLevel(); got != rns.LogDebug {
		t.Errorf("RNS logger level = %v, want LogDebug after Apply", got)
	}

	// A re-Apply on the same setup instance carries the new level.
	cfg.LogRNSLevel = "ERROR"
	setup.Apply(cfg, logger, nil, nil)
	if got := logger.GetLogLevel(); got != rns.LogError {
		t.Errorf("RNS logger level = %v, want LogError", got)
	}
}

// G16.17 /reload re-runs Apply on the hub's own LogSetup, so a changed
// log_level in the TOML applies immediately.
func TestReloadReconfiguresLogging(t *testing.T) {
	env := newHubTestEnv(t)
	env.setDestination(t)
	hub := env.hub
	link := &rns.Link{}

	dir := testutils.TempDir(t, "reload-log")
	cfgPath := writeTemp(t, dir, "rrcd.toml",
		"[hub]\nhub_name = 'rrc'\n\n[logging]\nlevel = 'DEBUG'\n")
	hub.Config.ConfigPath = &cfgPath
	hub.rnsLogger = nil

	if hub.logSetup.DebugEnabled() {
		t.Fatal("debug should be off before the reload")
	}

	outgoing := &OutgoingList{}
	hub.ReloadConfigAndRooms(link, nil, outgoing)

	if !hub.logSetup.DebugEnabled() {
		t.Error("the reload did not apply the DEBUG log level")
	}
}
