// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"
)

func TestMakeMsgType(t *testing.T) {
	t.Parallel()

	if got := makeMsgType(msgTypeVersionInfo); got != 0xac05 {
		t.Fatalf("makeMsgType(version)=%#x, want %#x", got, 0xac05)
	}
}

func TestWindowSizeRoundTrip(t *testing.T) {
	t.Parallel()

	rows, cols := 24, 80
	m := windowSizeMessage{Rows: &rows, Cols: &cols}
	raw, err := m.pack()
	if err != nil {
		t.Fatalf("pack error: %v", err)
	}

	var decoded windowSizeMessage
	if err := decoded.unpack(raw); err != nil {
		t.Fatalf("unpack error: %v", err)
	}
	if decoded.Rows == nil || *decoded.Rows != 24 {
		t.Fatalf("rows=%v", decoded.Rows)
	}
	if decoded.Cols == nil || *decoded.Cols != 80 {
		t.Fatalf("cols=%v", decoded.Cols)
	}
}

func TestExecuteCommandRoundTrip(t *testing.T) {
	t.Parallel()

	term := "xterm-256color"
	rows := 30
	m := executeCommandMessage{
		CommandLine: []string{"/bin/sh", "-lc", "echo hi"},
		PipeStdin:   true,
		PipeStdout:  false,
		PipeStderr:  true,
		TCFlags:     []any{1, 2, 3},
		Term:        &term,
		Rows:        &rows,
	}
	raw, err := m.pack()
	if err != nil {
		t.Fatalf("pack error: %v", err)
	}

	var decoded executeCommandMessage
	if err := decoded.unpack(raw); err != nil {
		t.Fatalf("unpack error: %v", err)
	}
	if len(decoded.CommandLine) != 3 || decoded.CommandLine[2] != "echo hi" {
		t.Fatalf("decoded cmdline=%v", decoded.CommandLine)
	}
	if !decoded.PipeStdin || decoded.PipeStdout != false || !decoded.PipeStderr {
		t.Fatalf("decoded pipe flags invalid: %+v", decoded)
	}
}

func TestVersionInfoRoundTrip(t *testing.T) {
	t.Parallel()

	m := versionInfoMessage{SoftwareVersion: "0.1.0", ProtocolVersion: protocolVersion}
	raw, err := m.pack()
	if err != nil {
		t.Fatalf("pack error: %v", err)
	}

	var decoded versionInfoMessage
	if err := decoded.unpack(raw); err != nil {
		t.Fatalf("unpack error: %v", err)
	}
	if decoded.SoftwareVersion != "0.1.0" || decoded.ProtocolVersion != protocolVersion {
		t.Fatalf("decoded=%+v", decoded)
	}
}

func TestErrorRoundTrip(t *testing.T) {
	t.Parallel()

	m := errorMessage{Message: "boom", Fatal: true, Data: map[string]any{"code": 5}}
	raw, err := m.pack()
	if err != nil {
		t.Fatalf("pack error: %v", err)
	}

	var decoded errorMessage
	if err := decoded.unpack(raw); err != nil {
		t.Fatalf("unpack error: %v", err)
	}
	if decoded.Message != "boom" || !decoded.Fatal {
		t.Fatalf("decoded=%+v", decoded)
	}
}

func TestCommandExitedRoundTrip(t *testing.T) {
	t.Parallel()

	m := commandExitedMessage{ReturnCode: 42}
	raw, err := m.pack()
	if err != nil {
		t.Fatalf("pack error: %v", err)
	}

	var decoded commandExitedMessage
	if err := decoded.unpack(raw); err != nil {
		t.Fatalf("unpack error: %v", err)
	}
	if decoded.ReturnCode != 42 {
		t.Fatalf("return code=%v", decoded.ReturnCode)
	}
}
