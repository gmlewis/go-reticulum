// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package rns

import "testing"

func TestLoggerStateDefaultsAndMutation(t *testing.T) {
	t.Parallel()

	state := newLoggerState()

	if got := state.GetLogLevel(); got != LogNotice {
		t.Fatalf("GetLogLevel() = %v, want %v", got, LogNotice)
	}
	if got := state.GetLogDest(); got != LogStdout {
		t.Fatalf("GetLogDest() = %v, want %v", got, LogStdout)
	}
	if state.GetAlwaysOverride() {
		t.Fatal("GetAlwaysOverride() = true, want false")
	}
	if state.GetCompactLogFmt() {
		t.Fatal("GetCompactLogFmt() = true, want false")
	}
	if got := state.GetLogFilePath(); got != "" {
		t.Fatalf("GetLogFilePath() = %q, want empty", got)
	}
	if got := state.GetLogCallback(); got != nil {
		t.Fatal("GetLogCallback() = non-nil, want nil")
	}

	state.SetAlwaysOverride(true)
	state.SetCompactLogFmt(true)
	state.SetLogLevel(LogDebug)
	state.SetLogFilePath("/tmp/logfile")
	state.SetLogDest(LogDestFile)

	var callbackCalled bool
	state.SetLogCallback(func(msg string) {
		callbackCalled = msg == "hello"
	})

	if !state.GetAlwaysOverride() {
		t.Fatal("GetAlwaysOverride() = false, want true")
	}
	if !state.GetCompactLogFmt() {
		t.Fatal("GetCompactLogFmt() = false, want true")
	}
	if got := state.GetLogLevel(); got != LogDebug {
		t.Fatalf("GetLogLevel() = %v, want %v", got, LogDebug)
	}
	if got := state.GetLogFilePath(); got != "/tmp/logfile" {
		t.Fatalf("GetLogFilePath() = %q, want %q", got, "/tmp/logfile")
	}
	if got := state.GetLogDest(); got != LogDestFile {
		t.Fatalf("GetLogDest() = %v, want %v", got, LogDestFile)
	}
	if got := state.GetLogCallback(); got == nil {
		t.Fatal("GetLogCallback() = nil, want function")
	} else {
		got("hello")
	}
	if !callbackCalled {
		t.Fatal("callback was not called")
	}
}
