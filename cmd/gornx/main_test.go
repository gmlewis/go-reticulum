// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

func TestNewRuntime(t *testing.T) {
	t.Parallel()

	rt := newRuntime(nil)
	if rt == nil {
		t.Fatal("newRuntime() returned nil")
	}
	if rt.app == nil {
		t.Fatal("newRuntime() did not initialize the app state")
	}
	if rt.logger == nil {
		t.Fatal("newRuntime() did not initialize a logger")
	}
}

func TestPrepareIdentity(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := testutils.TempDir(t, "gornx-id-")
	defer cleanup()

	rt := newRuntime(&appT{configDir: tmpDir})

	// 1. Create new identity
	idPath := filepath.Join(tmpDir, "test.id")
	id := rt.prepareIdentity(idPath)
	if id == nil {
		t.Fatal("prepareIdentity returned nil")
	}
	if _, err := os.Stat(idPath); err != nil {
		t.Errorf("identity file not created: %v", err)
	}

	// 2. Load existing identity
	id2 := rt.prepareIdentity(idPath)
	if id2.HexHash != id.HexHash {
		t.Errorf("loaded identity mismatch: got %v, want %v", id2.HexHash, id.HexHash)
	}
}

func TestResolveAllowedIdentitiesPath(t *testing.T) {
	t.Parallel()
	tmpDir, cleanup := testutils.TempDir(t, "gornx-allowed-")
	defer cleanup()

	// 1. Test ~/.rnx/allowed_identities
	rnxDir := filepath.Join(tmpDir, ".rnx")
	if err := os.MkdirAll(rnxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	allowedFile := filepath.Join(rnxDir, "allowed_identities")
	if err := os.WriteFile(allowedFile, []byte("hash"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := resolveAllowedIdentitiesPath(tmpDir)
	if got != allowedFile {
		t.Errorf("got %q, want %q", got, allowedFile)
	}

	// 2. Test ~/.config/rnx/allowed_identities (precedence)
	configRnxDir := filepath.Join(tmpDir, ".config", "rnx")
	if err := os.MkdirAll(configRnxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configAllowedFile := filepath.Join(configRnxDir, "allowed_identities")
	if err := os.WriteFile(configAllowedFile, []byte("hash"), 0o600); err != nil {
		t.Fatal(err)
	}

	got = resolveAllowedIdentitiesPath(tmpDir)
	if got != configAllowedFile {
		t.Errorf("got %q, want %q", got, configAllowedFile)
	}
}

func TestDecodeRequestPayload(t *testing.T) {
	t.Parallel()
	// Python request payload: [command_bytes, timeout, stdout_limit, stderr_limit, stdin_bytes]
	command := "echo hello"
	timeout := 15.5
	stdoutLimit := 1024
	stderrLimit := 2048
	stdin := []byte("input")

	payload := []any{
		[]byte(command),
		timeout,
		int64(stdoutLimit),
		int64(stderrLimit),
		stdin,
	}

	packed, err := rns.Pack(payload)
	if err != nil {
		t.Fatal(err)
	}

	gotCmd, gotTimeout, gotStdoutLimit, gotStderrLimit, gotStdin, err := decodeRequestPayload(packed)
	if err != nil {
		t.Fatalf("decodeRequestPayload failed: %v", err)
	}

	if gotCmd != command {
		t.Errorf("got command %q, want %q", gotCmd, command)
	}
	if gotTimeout != timeout {
		t.Errorf("got timeout %v, want %v", gotTimeout, timeout)
	}
	if gotStdoutLimit == nil || *gotStdoutLimit != stdoutLimit {
		t.Errorf("got stdout limit %v, want %v", gotStdoutLimit, stdoutLimit)
	}
	if gotStderrLimit == nil || *gotStderrLimit != stderrLimit {
		t.Errorf("got stderr limit %v, want %v", gotStderrLimit, stderrLimit)
	}
	if !bytes.Equal(gotStdin, stdin) {
		t.Errorf("got stdin %q, want %q", gotStdin, stdin)
	}
}

func TestHandleCommandRequestTruncation(t *testing.T) {
	t.Parallel()
	logger := rns.NewLogger()
	rt := newRuntime(nil)
	rt.logger = logger

	// Test stdout truncation to 5 bytes
	stdoutLimit := 5
	payload := []any{
		[]byte("echo 1234567890"),
		float64(5.0),
		int64(stdoutLimit),
		int64(0),
		[]byte{},
	}
	packed, _ := rns.Pack(payload)

	res := rt.handleCommandRequest("", packed, nil, nil, nil, time.Now())
	result := res.([]any)

	if result[0] != true {
		t.Errorf("executed = %v, want true", result[0])
	}
	stdout := result[2].([]byte)
	if string(stdout) != "12345" {
		t.Errorf("stdout = %q, want %q", string(stdout), "12345")
	}
	if result[4] != int64(11) { // 1234567890\n
		t.Errorf("total stdout length = %v, want 11", result[4])
	}
}

func TestHandleCommandRequestFailure(t *testing.T) {
	t.Parallel()
	logger := rns.NewLogger()
	rt := newRuntime(nil)
	rt.logger = logger

	// Test non-existent command
	payload := []any{
		[]byte("nonexistent_command_12345"),
		float64(5.0),
		int64(0),
		int64(0),
		[]byte{},
	}
	packed, _ := rns.Pack(payload)

	res := rt.handleCommandRequest("", packed, nil, nil, nil, time.Now())
	result := res.([]any)

	if result[0] != false {
		t.Errorf("executed = %v, want false", result[0])
	}
}
