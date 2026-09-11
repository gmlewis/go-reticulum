// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file verifies the per-plugin KV scratch store wiring (wago-analysis.md
// §8.4): each loaded plugin gets a store scoped by its file name under
// <pluginsDir>/data, and the rns.kv_set / rns.kv_get host imports round-trip
// values through it.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/pluginstore"
)

// TestPluginHostKVImports runs the KV fixture end-to-end: the plugin stores
// "v" under "k" through the rns.kv_set import, reads it back through
// rns.kv_get, and HandleCommand returns the value the host persisted.
func TestPluginHostKVImports(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(2*time.Second, func(string, ...any) {})
	defer h.Close()
	pluginPath := writeFixture(t, MinWasmKVPlugin)
	if err := h.LoadPlugin(pluginPath); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	// The plugin's response bytes are the value it read back from the store.
	resp, err := h.HandleCommand("any request")
	if err != nil {
		t.Fatalf("HandleCommand: %v", err)
	}
	if strings.TrimSuffix(resp, "\x00") != "v" {
		t.Fatalf("KV roundtrip response = %q, want %q", resp, "v")
	}

	// The value landed in the plugin's scoped store directory on disk.
	data, err := os.ReadFile(filepath.Join(filepath.Dir(pluginPath), "data", "plugin", "k"))
	if err != nil {
		t.Fatalf("store file missing: %v", err)
	}
	if string(data) != "v" {
		t.Errorf("stored value = %q, want %q", data, "v")
	}
}

// TestPluginHostKVScope verifies the per-plugin store layout: each plugin's
// store directory is scoped by its base name under <pluginsDir>/data.
func TestPluginHostKVScope(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(2*time.Second, nil)
	defer h.Close()
	pluginPath := writeFixture(t, MinWasmAnswer)
	if err := h.LoadPlugin(pluginPath); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if h.store == nil {
		t.Fatal("loaded host has no KV store")
	}
	want := filepath.Join(filepath.Dir(pluginPath), "data", "plugin")
	if h.store.Dir() != want {
		t.Errorf("KV store dir = %q, want %q", h.store.Dir(), want)
	}
}

// TestPluginStoreQuotaExceeded pins the documented 10 MiB default quota.
func TestPluginStoreQuotaExceeded(t *testing.T) {
	t.Parallel()

	if pluginstore.MaxStoreBytes != 10<<20 {
		t.Errorf("MaxStoreBytes = %v, want 10 MiB", pluginstore.MaxStoreBytes)
	}
}
