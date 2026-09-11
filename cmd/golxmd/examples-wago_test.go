// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file smoke-tests the shipped delivery-filter example from
// assets/wasm-plugins/ against the golxmd filter host.

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestExampleFilterAcceptWorks loads the shipped filter example and verifies
// it accepts a serialized inbound message.
func TestExampleFilterAcceptWorks(t *testing.T) {
	t.Parallel()

	h := NewFilterHost(2*time.Second, nil)
	defer h.Close()
	src, err := os.ReadFile(filepath.Join("..", "..", "assets", "wasm-plugins", "filter-accept", "filter-accept.wasm"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	dir := testutils.TempDir(t, "golxmd-filter")
	pluginPath := filepath.Join(dir, "filter-accept.wasm")
	if err := os.WriteFile(pluginPath, src, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := h.LoadPlugin(pluginPath); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	ok, err := h.HandleFilter([]byte(`{"content":"hello"}`))
	if err != nil {
		t.Fatalf("HandleFilter: %v", err)
	}
	if !ok {
		t.Error("filter-accept example dropped the message, want accepted")
	}
}
