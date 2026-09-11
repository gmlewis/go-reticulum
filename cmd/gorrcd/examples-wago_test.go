// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file smoke-tests the shipped example plugins from
// assets/wasm-plugins/ so the repository's example binaries are guaranteed
// to work with the tools that document them.

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// examplePath resolves an example plugin path relative to this package
// (cmd/gorrcd -> repo root -> assets).
func examplePath(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join("..", "..", rel)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("example plugin missing: %v", err)
	}
	return path
}

// TestExamplePluginsWork loads the shipped echo and kv-demo examples and
// exercises them through the gorrcd plugin host.
func TestExamplePluginsWork(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(2*time.Second, nil)
	defer h.Close()

	if err := h.LoadPlugin(examplePath(t, "assets/wasm-plugins/echo/echo.wasm")); err != nil {
		t.Fatalf("LoadPlugin(echo): %v", err)
	}
	resp, err := h.HandleCommand("echo hello")
	if err != nil {
		t.Fatalf("HandleCommand(echo): %v", err)
	}
	if len(resp) == 0 {
		t.Error("echo example returned an empty response")
	}
	h.Close()

	kv := NewPluginHost(2*time.Second, nil)
	defer kv.Close()
	if err := kv.LoadPlugin(examplePath(t, "assets/wasm-plugins/kv-demo/kv-demo.wasm")); err != nil {
		t.Fatalf("LoadPlugin(kv-demo): %v", err)
	}
	resp, err = kv.HandleCommand("kv-demo")
	if err != nil {
		t.Fatalf("HandleCommand(kv-demo): %v", err)
	}
	if resp != "v" {
		t.Errorf("kv-demo response = %q, want %q", resp, "v")
	}
}
