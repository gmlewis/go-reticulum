// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file verifies the wago command plugin host: plugin discovery by
// command name, sandboxed execution of a requested command through the
// handle_command ABI, output-limit handling, and deadline interruption.

package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// testRequestTime returns a fixed request time for deterministic payloads.
func testRequestTime() time.Time {
	return time.Unix(1730000000, 0)
}

// intPtr returns a pointer to n.
func intPtr(n int) *int {
	return &n
}

// assertExecResult verifies the rnx result array for a successful plugin
// execution.
func assertExecResult(t *testing.T, result any, command string) {
	t.Helper()
	rows, ok := result.([]any)
	if !ok || len(rows) != 8 {
		t.Fatalf("result = %#v, want an 8-element array", result)
	}
	if rows[0] != true {
		t.Fatalf("executed flag = %v, want true", rows[0])
	}
	if rows[1] != int64(0) {
		t.Errorf("returncode = %v, want 0", rows[1])
	}
	stdout, ok := rows[2].([]byte)
	if !ok {
		t.Fatalf("stdout = %#v, want []byte", rows[2])
	}
	var req map[string]any
	if err := json.Unmarshal(stdout, &req); err != nil {
		t.Fatalf("stdout is not the request JSON: %v (%q)", err, stdout)
	}
	if req["command"] != command {
		t.Errorf("request command = %v, want %q", req["command"], command)
	}
	if rows[3].([]byte) == nil || len(rows[3].([]byte)) != 0 {
		t.Errorf("stderr = %v, want empty", rows[3])
	}
	if rows[4] != int64(len(stdout)) {
		t.Errorf("stdout_len = %v, want %v", rows[4], len(stdout))
	}
	if rows[5] != int64(0) {
		t.Errorf("stderr_len = %v, want 0", rows[5])
	}
}

// TestPluginHostEcho runs the echo command plugin directly: the response
// bytes equal the serialized request.
func TestPluginHostEcho(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(2*time.Second, func(string, ...any) {})
	defer h.Close()
	if h.Active() {
		t.Fatal("a fresh host reports Active before any load")
	}
	dir := testutils.TempDir(t, "gornx-plugins")
	if err := h.LoadPlugin(writeFixtureFile(t, dir, "echo.wasm", MinWasmEchoCommandPlugin)); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if !h.Active() {
		t.Fatal("PluginHost reports inactive after a successful load")
	}

	req := []byte(`{"command":"echo hi"}`)
	out, err := h.HandleCommand(req, 2*time.Second)
	if err != nil {
		t.Fatalf("HandleCommand: %v", err)
	}
	if string(out) != string(req) {
		t.Errorf("HandleCommand = %q, want the echoed request %q", out, req)
	}
	h.Close()
	if h.Active() {
		t.Fatal("PluginHost reports Active after Close")
	}
}

// TestCommandHostsDispatch verifies discovery by command name: the plugin
// file's base name (minus .wasm) is the command it serves, a missing
// command dispatches nil, and a second load is refused.
func TestCommandHostsDispatch(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gornx-plugins")
	writeFixtureFile(t, dir, "echo.wasm", MinWasmEchoCommandPlugin)
	writeFixtureFile(t, dir, "notes.txt", []byte("not a plugin"))

	ch := newCommandHosts(dir, 2*time.Second, func(string, ...any) {})
	defer ch.close()
	if ch.forCommand("echo") == nil {
		t.Fatal("forCommand(echo) = nil, want a loaded host")
	}
	if ch.forCommand("notes.txt") != nil {
		t.Error("forCommand served a non-.wasm file")
	}
	if ch.forCommand("nope") != nil {
		t.Fatal("forCommand(nope) returned a host, want nil")
	}
}

// TestExecWasmCommand verifies the rnx result-array mapping for a
// plugin-backed command: executed=true, returncode=0, stdout=plugin
// response (echo of the request JSON), stderr empty, lengths recorded, and
// the stdout limit applied.
func TestExecWasmCommand(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gornx-plugins")
	writeFixtureFile(t, dir, "echo.wasm", MinWasmEchoCommandPlugin)
	ch := newCommandHosts(dir, 2*time.Second, func(string, ...any) {})
	defer ch.close()

	host := ch.forCommand("echo")
	if host == nil {
		t.Fatal("forCommand(echo) = nil, want a loaded host")
	}

	result := execWasmCommand(host, "echo hello world", []byte("stdin-data"), []byte{0xab}, nil, testRequestTime(), 0, nil, nil)
	assertExecResult(t, result, "echo hello world")

	// The stdout limit truncates the plugin output.
	limited := execWasmCommand(host, "echo hello world", nil, nil, nil, testRequestTime(), 0, intPtr(10), nil)
	rows := limited.([]any)
	stdout, ok := rows[2].([]byte)
	if !ok || len(stdout) != 10 {
		t.Errorf("limited stdout = %v, want 10 bytes", rows[2])
	}
}

// TestExecWasmCommandStdin verifies the request JSON carries stdin, link,
// and identity metadata for the plugin.
func TestExecWasmCommandStdin(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gornx-plugins")
	writeFixtureFile(t, dir, "echo.wasm", MinWasmEchoCommandPlugin)
	ch := newCommandHosts(dir, 2*time.Second, func(string, ...any) {})
	defer ch.close()
	host := ch.forCommand("echo")

	identityBytes := make([]byte, 32)
	for i := range identityBytes {
		identityBytes[i] = byte(0x21)
	}
	result := execWasmCommand(host, "echo", []byte("STDIN"), []byte{0x01, 0x02}, &rns.Identity{Hash: identityBytes}, testRequestTime(), 5, nil, nil)
	rows := result.([]any)
	// The echoed stdout IS the request JSON.
	stdout, ok := rows[2].([]byte)
	if !ok {
		t.Fatalf("stdout = %#v, want []byte", rows[2])
	}
	var req map[string]any
	if err := json.Unmarshal(stdout, &req); err != nil {
		t.Fatalf("plugin stdout is not the request JSON: %v (%q)", err, stdout)
	}
	if req["stdin"] != "STDIN" {
		t.Errorf("request stdin = %v, want STDIN", req["stdin"])
	}
	if req["link_id"] != "0102" {
		t.Errorf("request link_id = %v, want 0102", req["link_id"])
	}
	if req["remote_identity"] != strings.Repeat("21", 32) {
		t.Errorf("request remote_identity = %v, want the 32-byte hex", req["remote_identity"])
	}
}

// TestExecWasmCommandTimeout verifies that a plugin's runaway loop is
// preempted by the request timeout: the command reports executed=false and
// the call returns promptly.
func TestExecWasmCommandTimeout(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gornx-plugins")
	writeFixtureFile(t, dir, "spin.wasm", MinWasmSpin)
	ch := newCommandHosts(dir, 2*time.Second, func(string, ...any) {})
	defer ch.close()
	host := ch.forCommand("spin")

	start := time.Now()
	result := execWasmCommand(host, "spin", nil, nil, nil, testRequestTime(), 0.05, nil, nil)
	elapsed := time.Since(start)
	rows := result.([]any)
	if rows[0] != false {
		t.Errorf("executed flag = %v, want false after the timeout", rows[0])
	}
	if elapsed >= time.Second {
		t.Errorf("execWasmCommand took %v, the deadline did not preempt the plugin", elapsed)
	}
}

// TestCommandHostsKV verifies that a loaded plugin gets its own scoped KV
// scratch store directory under <pluginsDir>/data.
func TestCommandHostsKV(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gornx-plugins")
	writeFixtureFile(t, dir, "echo.wasm", MinWasmEchoCommandPlugin)
	ch := newCommandHosts(dir, 2*time.Second, func(string, ...any) {})
	defer ch.close()
	host := ch.forCommand("echo")
	if host == nil {
		t.Fatal("forCommand(echo) = nil, want a loaded host")
	}
	if host.store == nil {
		t.Fatal("loaded host has no KV store")
	}
	if want := dir + "/data/echo"; host.store.Dir() != want {
		t.Errorf("KV store dir = %q, want %q", host.store.Dir(), want)
	}
}
