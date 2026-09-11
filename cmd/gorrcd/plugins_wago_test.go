// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file verifies the wago plugin host: loading embedded wasm fixtures,
// echoing slash commands through the guest, and interrupting a runaway
// native loop through the invocation deadline.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/rrc"
	"github.com/gmlewis/go-reticulum/rrc/cbor"
)

// writeFixture writes fixture bytes into a fresh temp file and returns the
// path.
func writeFixture(t *testing.T, wasm []byte) string {
	t.Helper()
	path := filepath.Join(tempDir(t), "plugin.wasm")
	if err := os.WriteFile(path, wasm, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestPluginHostWagoAnswerFixture loads the minimal "answer" module and
// calls its export directly: proof that the runtime compiles and executes
// wasm in-process.
func TestPluginHostWagoAnswerFixture(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(time.Second, nil)
	defer h.Close()
	if err := h.LoadPlugin(writeFixture(t, MinWasmAnswer)); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if !h.Active() {
		t.Fatal("PluginHost reports inactive after a successful load")
	}
	out, err := h.invoke("answer")
	if err != nil {
		t.Fatalf("invoke(answer): %v", err)
	}
	if len(out) != 1 || out[0].I32() != 42 {
		t.Errorf("invoke(answer) = %v, want [42]", out)
	}
}

// TestPluginHostWagoCommandEcho loads the echo-command plugin and verifies
// the full alloc / write / handle_command / read protocol: the response
// bytes equal the request, the host stays active, a second load is refused,
// and Close deactivates the host.
func TestPluginHostWagoCommandEcho(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(2*time.Second, func(string, ...any) {})
	defer h.Close()

	if h.Active() {
		t.Fatal("a fresh host reports Active before any load")
	}
	if err := h.LoadPlugin(writeFixture(t, MinWasmCommandPlugin)); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if !h.Active() {
		t.Fatal("PluginHost reports inactive after a successful load")
	}
	if err := h.LoadPlugin(writeFixture(t, MinWasmAnswer)); err == nil {
		t.Fatal("a second LoadPlugin on a loaded host succeeded, want an error")
	}

	const cmdLine = "custom hello world"
	resp, err := h.HandleCommand(cmdLine)
	if err != nil {
		t.Fatalf("HandleCommand(%q): %v", cmdLine, err)
	}
	if resp != cmdLine {
		t.Errorf("HandleCommand(%q) = %q, want the echoed input", cmdLine, resp)
	}

	h.Close()
	if h.Active() {
		t.Fatal("PluginHost reports Active after Close")
	}
	if _, err := h.HandleCommand(cmdLine); err == nil {
		t.Fatal("HandleCommand after Close succeeded, want an error")
	}
}

// TestPluginHostWagoSpinTimeout loads the infinite-loop module and verifies
// that the invocation deadline interrupts the native loop: the call returns
// context.DeadlineExceeded and does not block. The 50 ms budget leaves the
// interrupt mechanism (loop safepoints or signals) ample headroom; the
// generous wall-clock bound only guards against a hung process, matching
// the engine's own cancellation tests which observe about 20 ms.
func TestPluginHostWagoSpinTimeout(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(50*time.Millisecond, nil)
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

	// The instance stays usable after the interrupted call: a missing
	// export still reports a clean signature error instead of a stuck
	// instance.
	if _, err := h.invoke("no_such_export"); err == nil {
		t.Error("invoke on a missing export succeeded, want an error")
	}
}

// TestPluginHostWagoLogImport verifies the rns.log host import: the guest
// calls it with a (ptr, len) message and the host logger receives the
// guest's linear-memory bytes.
func TestPluginHostWagoLogImport(t *testing.T) {
	t.Parallel()

	var logged []string
	h := NewPluginHost(time.Second, func(format string, args ...any) {
		logged = append(logged, fmt.Sprintf(format, args...))
	})
	defer h.Close()
	if err := h.LoadPlugin(writeFixture(t, MinWasmLogPlugin)); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if _, err := h.invoke("log_hi"); err != nil {
		t.Fatalf("invoke(log_hi): %v", err)
	}
	if len(logged) != 1 || logged[0] != "plugin: hi" {
		t.Errorf("logged = %q, want exactly [plugin: hi]", logged)
	}
}

// TestPluginHostWagoDenyByDefault verifies that a module importing a
// capability the host does not wire (rns.kv_get in Milestone 1) fails to
// instantiate: the deny-by-default import surface stays closed.
func TestPluginHostWagoDenyByDefault(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(time.Second, nil)
	defer h.Close()
	err := h.LoadPlugin(writeFixture(t, MinWasmDeniedPlugin))
	if err == nil {
		t.Fatal("LoadPlugin of an unwired-import module succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "kv_get") {
		t.Errorf("LoadPlugin error = %v, want it to name the denied import kv_get", err)
	}
	if h.Active() {
		t.Error("host reports Active after a failed load, want inactive")
	}
}

// TestPluginHostWagoEndToEndHook runs the full Milestone 1 path: an unknown
// slash command flows through pluginCommandHook into the sandboxed echo
// plugin, and the response is emitted to the requesting link as a NOTICE.
func TestPluginHostWagoEndToEndHook(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(2*time.Second, nil)
	defer h.Close()
	if err := h.LoadPlugin(writeFixture(t, MinWasmCommandPlugin)); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	identity := make([]byte, 32)
	for i := range identity {
		identity[i] = byte(0x21)
	}
	mh := rrc.NewMessageHelper(rrc.MessageHooks{
		IdentityHash: func() []byte { return identity },
		StatsInc:     func(string, int) {},
		SendPacket: func(_ *rns.Link, payload []byte) error {
			return nil
		},
	})
	link := &rns.Link{}
	room := "lounge"
	outgoing := &rrc.OutgoingList{}
	hook := pluginCommandHook(mh, []*PluginHost{h}, func(string, ...any) {})

	if !hook(link, nil, &room, []string{"custom", "hello"}, outgoing) {
		t.Fatal("pluginCommandHook did not handle /custom with an active plugin")
	}
	if len(outgoing.Queue) != 1 {
		t.Fatalf("outgoing queue holds %v envelope(s), want 1", len(outgoing.Queue))
	}
	decoded, err := cbor.Decode(outgoing.Queue[0].Payload)
	if err != nil {
		t.Fatalf("queued payload does not decode: %v", err)
	}
	m, ok := decoded.(*cbor.Map)
	if !ok {
		t.Fatalf("queued payload decodes to %T, want *cbor.Map", decoded)
	}
	if body, ok := m.Get(int64(6)); !ok || body != "custom hello" {
		t.Errorf("notice body = %v (%T), want %q", body, body, "custom hello")
	}
	if msgType, ok := m.Get(int64(1)); !ok || msgType != int64(21) {
		t.Errorf("envelope type = %v, want 21 (NOTICE)", msgType)
	}

	// Without an active host the hook stays false.
	idle := NewPluginHost(time.Second, nil)
	defer idle.Close()
	if pluginCommandHook(mh, []*PluginHost{idle}, nil)(link, nil, &room, []string{"custom"}, outgoing) {
		t.Error("pluginCommandHook handled a command with an inactive host, want false")
	}
}
