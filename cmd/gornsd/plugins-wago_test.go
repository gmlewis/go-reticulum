// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file verifies the wago announce observer host: loading embedded wasm
// fixtures, delivering serialized announce events through the on_announce
// ABI, and deadline interruption.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// writeObserverFixture writes fixture bytes under dir and returns the path.
func writeObserverFixture(t *testing.T, dir, name string, wasm []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, wasm, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestObserverHostAckAndIssue loads the acknowledged and issue fixtures:
// status 0 announces cleanly, a nonzero status reports an error, a second
// load is refused, and Close deactivates the host.
func TestObserverHostAckAndIssue(t *testing.T) {
	t.Parallel()

	ack := NewObserverHost(2*time.Second, func(string, ...any) {})
	defer ack.Close()
	if ack.Active() {
		t.Fatal("a fresh host reports Active before any load")
	}
	if err := ack.LoadPlugin(writeObserverFixture(t, testutils.TempDir(t, "gornsd-observers"), "observer.wasm", MinWasmObserverAck)); err != nil {
		t.Fatalf("LoadPlugin(ack): %v", err)
	}
	if !ack.Active() {
		t.Fatal("ObserverHost reports inactive after a successful load")
	}
	if err := ack.LoadPlugin(writeObserverFixture(t, testutils.TempDir(t, "gornsd-observers"), "other.wasm", MinWasmObserverIssue)); err == nil {
		t.Fatal("a second LoadPlugin on a loaded host succeeded, want an error")
	}
	if err := ack.HandleAnnounce([]byte(`{"destination_hash":"aabb"}`)); err != nil {
		t.Fatalf("HandleAnnounce(ack): %v", err)
	}
	ack.Close()
	if ack.Active() {
		t.Fatal("ObserverHost reports Active after Close")
	}
	if err := ack.HandleAnnounce([]byte(`{}`)); err == nil {
		t.Fatal("HandleAnnounce after Close succeeded, want an error")
	}

	issue := NewObserverHost(2*time.Second, nil)
	defer issue.Close()
	if err := issue.LoadPlugin(writeObserverFixture(t, testutils.TempDir(t, "gornsd-observers"), "issue.wasm", MinWasmObserverIssue)); err != nil {
		t.Fatalf("LoadPlugin(issue): %v", err)
	}
	err := issue.HandleAnnounce([]byte(`{"destination_hash":"aabb"}`))
	if err == nil {
		t.Fatal("issue plugin's nonzero status produced no error, want one")
	}
	if !strings.Contains(err.Error(), "7") {
		t.Errorf("issue error = %v, want it to mention status 7", err)
	}
}

// TestObserverHostSpinTimeout verifies that a plugin's runaway loop is
// preempted by the execution budget: the call returns context.DeadlineExceeded
// promptly.
func TestObserverHostSpinTimeout(t *testing.T) {
	t.Parallel()

	h := NewObserverHost(50*time.Millisecond, nil)
	defer h.Close()
	if err := h.LoadPlugin(writeObserverFixture(t, testutils.TempDir(t, "gornsd-observers"), "spin.wasm", MinWasmSpin)); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	start := time.Now()
	_, err := h.invoke("spin", 50*time.Millisecond)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invoke(spin) error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed >= time.Second {
		t.Errorf("invoke(spin) took %v, the deadline did not preempt the plugin", elapsed)
	}
}

// TestAnnounceEventJSON verifies the serialized announce payload carries the
// owned leaf fields an observer may inspect.
func TestAnnounceEventJSON(t *testing.T) {
	t.Parallel()

	identity := make([]byte, 32)
	for i := range identity {
		identity[i] = byte(0x21)
	}
	data := announceEventJSON([]byte{0xaa, 0xbb}, &rns.Identity{Hash: identity}, []byte("node name"), true, 1730000000)
	if !strings.Contains(string(data), `"destination_hash":"aabb"`) {
		t.Errorf("announce event = %s, want the destination hash", data)
	}
	if !strings.Contains(string(data), `"app_data":"node name"`) {
		t.Errorf("announce event = %s, want the app data", data)
	}
	if !strings.Contains(string(data), `"is_path_response":true`) {
		t.Errorf("announce event = %s, want the path-response flag", data)
	}
}

// TestSetupAnnounceHostsWago runs the full observer path: hosts load from
// the plugins directory, one announce handler is registered on the
// transport, and dispatching an announce through it reaches the plugins
// without panicking (plugin issues are logged, not fatal).
func TestSetupAnnounceHostsWago(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gornsd-observers")
	pluginsDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	writeObserverFixture(t, pluginsDir, "observer.wasm", MinWasmObserverAck)
	writeObserverFixture(t, pluginsDir, "issue.wasm", MinWasmObserverIssue)

	ts := rns.NewTransportSystem(testSilentRNSLogger())
	hosts := setupAnnounceHosts(ts, dir, testSilentRNSLogger())
	defer closeAnnounceHosts(hosts)
	if len(hosts) != 2 {
		t.Fatalf("setupAnnounceHosts loaded %v host(s), want 2", len(hosts))
	}
	if got := len(ts.AnnounceHandlers()); got != 1 {
		t.Fatalf("announce handlers registered = %v, want 1", got)
	}

	// Dispatch a live announce through the registered handler: both hosts
	// receive the serialized event; the issue plugin's nonzero status is
	// logged, not fatal.
	handler := ts.AnnounceHandlers()[0]
	identity := &rns.Identity{Hash: make([]byte, 32)}
	handler.ReceivedAnnounceWithContext([]byte{0xaa, 0xbb}, identity, []byte("node name"), true)
}

// TestObserverHostKVScope verifies the per-plugin store layout: each
// observer's store directory is scoped by its base name under
// <pluginsDir>/data.
func TestObserverHostKVScope(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gornsd-observers")
	h := NewObserverHost(2*time.Second, nil)
	defer h.Close()
	if err := h.LoadPlugin(writeObserverFixture(t, dir, "observer.wasm", MinWasmObserverAck)); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if h.store == nil {
		t.Fatal("loaded observer host has no KV store")
	}
	if want := dir + "/data/observer"; h.store.Dir() != want {
		t.Errorf("KV store dir = %q, want %q", h.store.Dir(), want)
	}
}
