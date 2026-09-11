// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)

// This file verifies the stub plugin host: without the wago build tag the
// host is permanently inactive, every command returns an error, and the
// custom-command hook stays false so unknown commands behave exactly as
// before.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPluginHostStubInactive verifies that the stub build never activates a
// plugin host and never handles commands.
func TestPluginHostStubInactive(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(50*time.Millisecond, nil)
	if h == nil {
		t.Fatal("NewPluginHost returned nil")
	}
	if h.Active() {
		t.Fatal("stub PluginHost reports Active, want inactive")
	}

	wasmPath := filepath.Join(tempDir(t), "plugin.wasm")
	if err := os.WriteFile(wasmPath, MinWasmCommandPlugin, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := h.LoadPlugin(wasmPath); err == nil {
		t.Fatal("stub LoadPlugin succeeded, want an error")
	}
	if h.Active() {
		t.Fatal("stub PluginHost became Active after LoadPlugin")
	}

	resp, err := h.HandleCommand("custom hello")
	if err == nil {
		t.Fatalf("stub HandleCommand(%q) returned (%q, nil), want an error", "custom hello", resp)
	}
	if resp != "" {
		t.Errorf("stub HandleCommand response = %q, want empty", resp)
	}

	h.Close()
	if h.Active() {
		t.Fatal("stub PluginHost reports Active after Close")
	}
}

// TestPluginHostHookInactive verifies that the custom-command hook built
// over a plugin host with no loaded plugin returns false (the unknown
// command stays unknown).
func TestPluginHostHookInactive(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(time.Second, nil)
	hook := pluginCommandHook(nil, []*PluginHost{h}, func(string, ...any) {})
	if hook(nil, nil, nil, []string{"custom", "hello"}, nil) {
		t.Fatal("pluginCommandHook handled a command with an inactive host, want false")
	}
	if hook(nil, nil, nil, nil, nil) {
		t.Fatal("pluginCommandHook handled a command with no hosts, want false")
	}
}

// TestPluginHostsScansDirectory verifies that newPluginHosts scans the
// plugins directory, ignores non-.wasm files, and logs (but survives) load
// errors.
func TestPluginHostsScansDirectory(t *testing.T) {
	t.Parallel()

	dir := tempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not wasm"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	logCalls := 0
	hosts := newPluginHosts(dir, time.Second, func(string, ...any) { logCalls++ })
	if len(hosts) != 0 {
		t.Errorf("newPluginHosts loaded %v host(s) from a plugin-less directory, want 0", len(hosts))
	}
	for _, h := range hosts {
		h.Close()
	}

	// The stub build cannot load any plugin, even a valid .wasm file.
	if err := os.WriteFile(filepath.Join(dir, "plugin.wasm"), MinWasmAnswer, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	hosts = newPluginHosts(dir, time.Second, func(string, ...any) { logCalls++ })
	for _, h := range hosts {
		if h.Active() {
			t.Error("newPluginHosts produced an active host in the stub build")
		}
		h.Close()
	}
	if len(hosts) != 0 {
		t.Errorf("newPluginHosts loaded %v host(s) in the stub build, want 0", len(hosts))
	}
	if logCalls == 0 {
		t.Error("newPluginHosts logged no diagnostics for failed loads")
	}
}

// TestErrPluginsNotLinked documents the stub-build error sentinel.
func TestErrPluginsNotLinked(t *testing.T) {
	t.Parallel()

	if !errors.Is(errPluginsNotLinked, errPluginsNotLinked) {
		t.Fatal("errPluginsNotLinked does not match itself")
	}
	if errPluginsNotLinked == nil {
		t.Fatal("errPluginsNotLinked is nil, want a sentinel error")
	}
}
