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
	cfg.LogFile = strPtr(logPath)

	setup := ConfigureLogging(cfg, rns.NewLogger(), nil, nil)
	if setup.File == nil || *setup.File != logPath {
		t.Fatalf("setup.File = %v", setup.File)
	}
	if setup.Writer == nil {
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
	if setup.Writer != os.Stderr {
		t.Fatalf("console-only writer = %T, want *os.File(stderr)", setup.Writer)
	}
}

func TestConfigureLoggingNoWritersDiscards(t *testing.T) {
	cfg := DefaultHubConfig()
	cfg.LogConsole = false
	cfg.LogFile = nil
	setup := ConfigureLogging(cfg, nil, nil, nil)
	if setup.Writer != nil {
		t.Fatalf("writer = %v, want nil", setup.Writer)
	}
	// With no writers, the hub's own log output goes to io.Discard.
	if w := log.Writer(); w != io.Discard {
		t.Fatalf("log.Writer() = %T, want io.Discard", w)
	}
	defer log.SetOutput(io.Discard)
}
