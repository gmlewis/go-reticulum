// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file verifies the per-plugin KV scratch store wiring for the LXMF
// filter host: each loaded filter gets a store scoped by its file name under
// <pluginsDir>/data.

package main

import (
	"path/filepath"
	"testing"
	"time"
)

// TestFilterHostKVScope verifies the per-plugin store layout: each filter's
// store directory is scoped by its base name under <pluginsDir>/data.
func TestFilterHostKVScope(t *testing.T) {
	t.Parallel()

	h := NewFilterHost(2*time.Second, nil)
	defer h.Close()
	pluginPath := writeFixture(t, MinWasmFilterAccept)
	if err := h.LoadPlugin(pluginPath); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if h.store == nil {
		t.Fatal("loaded filter host has no KV store")
	}
	want := filepath.Join(filepath.Dir(pluginPath), "data", "plugin")
	if h.store.Dir() != want {
		t.Errorf("KV store dir = %q, want %q", h.store.Dir(), want)
	}
}
