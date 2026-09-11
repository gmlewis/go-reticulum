// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build wago && (linux || darwin || windows) && (amd64 || arm64)

// This file smoke-tests the shipped announce-observer example from
// assets/wasm-plugins/ against the gornsd observer host.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/rns"
	"github.com/gmlewis/go-reticulum/testutils"
)

// TestExampleAnnounceObserverWorks loads the shipped observer example and
// acknowledges a serialized announce through it.
func TestExampleAnnounceObserverWorks(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile(filepath.Join("..", "..", "assets", "wasm-plugins", "announce-observer", "announce-observer.wasm"))
	if err != nil {
		t.Fatalf("read example: %v", err)
	}
	dir := testutils.TempDir(t, "gornsd-observers")
	pluginsDir := filepath.Join(dir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginsDir, "announce-observer.wasm"), src, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ts := rns.NewTransportSystem(testSilentRNSLogger())
	hosts := setupAnnounceHosts(ts, dir, testSilentRNSLogger())
	defer closeAnnounceHosts(hosts)
	if len(hosts) != 1 {
		t.Fatalf("setupAnnounceHosts loaded %v host(s), want 1", len(hosts))
	}
	ts.AnnounceHandlers()[0].ReceivedAnnounceWithContext([]byte{0xaa}, nil, []byte("node"), false)
}
