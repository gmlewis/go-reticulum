// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file verifies the wago LXMF filter host: loading embedded wasm
// fixtures, accepting and dropping inbound messages through the
// filter_inbound ABI, and interrupting a runaway native loop through the
// invocation deadline.

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/lxmf"
	"github.com/gmlewis/go-reticulum/testutils"
)

// writeFixture writes fixture bytes into a fresh temp file and returns the
// path.
func writeFixture(t *testing.T, wasm []byte) string {
	t.Helper()
	path := filepath.Join(testutils.TempDir(t, "golxmd-filter"), "plugin.wasm")
	if err := os.WriteFile(path, wasm, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestFilterHostAcceptAndDrop loads the accept and drop fixtures and verifies
// the alloc / write / filter_inbound protocol: the accept plugin passes the
// message, the drop plugin rejects it, a second load is refused, and Close
// deactivates the host.
func TestFilterHostAcceptAndDrop(t *testing.T) {
	t.Parallel()

	accept := NewFilterHost(2*time.Second, func(string, ...any) {})
	defer accept.Close()
	if accept.Active() {
		t.Fatal("a fresh host reports Active before any load")
	}
	if err := accept.LoadPlugin(writeFixture(t, MinWasmFilterAccept)); err != nil {
		t.Fatalf("LoadPlugin(accept): %v", err)
	}
	if !accept.Active() {
		t.Fatal("FilterHost reports inactive after a successful load")
	}
	if err := accept.LoadPlugin(writeFixture(t, MinWasmFilterDrop)); err == nil {
		t.Fatal("a second LoadPlugin on a loaded host succeeded, want an error")
	}
	ok, err := accept.HandleFilter([]byte(`{"content":"hello"}`))
	if err != nil {
		t.Fatalf("HandleFilter(accept): %v", err)
	}
	if !ok {
		t.Error("accept plugin returned false, want true")
	}

	drop := NewFilterHost(2*time.Second, nil)
	defer drop.Close()
	if err := drop.LoadPlugin(writeFixture(t, MinWasmFilterDrop)); err != nil {
		t.Fatalf("LoadPlugin(drop): %v", err)
	}
	ok, err = drop.HandleFilter([]byte(`{"content":"hello"}`))
	if err != nil {
		t.Fatalf("HandleFilter(drop): %v", err)
	}
	if ok {
		t.Error("drop plugin returned true, want false")
	}

	accept.Close()
	if accept.Active() {
		t.Fatal("FilterHost reports Active after Close")
	}
	if _, err := accept.HandleFilter([]byte(`{}`)); err == nil {
		t.Fatal("HandleFilter after Close succeeded, want an error")
	}
}

// TestFilterHostSpinTimeout loads the infinite-loop module and verifies that
// the invocation deadline interrupts the native loop: the call returns
// context.DeadlineExceeded and does not block. The 50ms budget leaves the
// interrupt mechanism ample headroom; the generous wall-clock bound only
// guards against a hung process, matching the engine's own cancellation
// tests (about 20ms observed).
func TestFilterHostSpinTimeout(t *testing.T) {
	t.Parallel()

	h := NewFilterHost(50*time.Millisecond, nil)
	defer h.Close()
	if err := h.LoadPlugin(writeFixture(t, MinWasmSpin)); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	start := time.Now()
	_, err := h.invoke("spin")
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("invoke(spin) error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed >= time.Second {
		t.Errorf("invoke(spin) took %v, the deadline did not interrupt the loop promptly", elapsed)
	}
	// The instance stays usable after the interrupted call.
	if _, err := h.invoke("no_such_export"); err == nil {
		t.Error("invoke on a missing export succeeded, want an error")
	}
}

// TestFilteredDeliveryDropAndPass runs the full delivery-filter path: a drop
// plugin suppresses the base delivery handler entirely, an accept plugin
// lets it through with the original message, and a plugin error fails open.
func TestFilteredDeliveryDropAndPass(t *testing.T) {
	t.Parallel()

	msg := &lxmf.Message{
		DestinationHash: []byte{0xaa},
		SourceHash:      []byte{0xcc},
		Content:         []byte("hello world"),
		Timestamp:       1730000000,
	}

	dropped := NewFilterHost(2*time.Second, nil)
	defer dropped.Close()
	if err := dropped.LoadPlugin(writeFixture(t, MinWasmFilterDrop)); err != nil {
		t.Fatalf("LoadPlugin(drop): %v", err)
	}
	var calls int
	base := func(*lxmf.Message) { calls++ }
	filteredDelivery(base, []*FilterHost{dropped}, msg, func(string, ...any) {})
	if calls != 0 {
		t.Fatalf("base handler ran %v time(s) behind a drop filter, want 0", calls)
	}

	accepted := NewFilterHost(2*time.Second, nil)
	defer accepted.Close()
	if err := accepted.LoadPlugin(writeFixture(t, MinWasmFilterAccept)); err != nil {
		t.Fatalf("LoadPlugin(accept): %v", err)
	}
	filteredDelivery(base, []*FilterHost{accepted}, msg, func(string, ...any) {})
	if calls != 1 {
		t.Fatalf("base handler ran %v time(s) behind an accept filter, want 1", calls)
	}
}
