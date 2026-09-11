// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)

// This file verifies the stub command plugin host: without the wago build
// tag no plugin host activates and every requested command keeps its
// existing raw-exec path.

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/gmlewis/go-reticulum/testutils"
)

// TestPluginHostStubInactive verifies that the stub build never activates a
// plugin host.
func TestPluginHostStubInactive(t *testing.T) {
	t.Parallel()

	h := NewPluginHost(50*time.Millisecond, nil)
	if h == nil {
		t.Fatal("NewPluginHost returned nil")
	}
	if h.Active() {
		t.Fatal("stub PluginHost reports Active, want inactive")
	}
	if err := h.LoadPlugin(testutils.TempDir(t, "gornx-plugins") + "/echo.wasm"); err == nil {
		t.Fatal("stub LoadPlugin succeeded, want an error")
	}
	if _, err := h.HandleCommand([]byte(`{"command":"echo hi"}`), time.Second); err == nil {
		t.Fatal("stub HandleCommand succeeded, want an error")
	}
	h.Close()
	if h.Active() {
		t.Fatal("stub PluginHost reports Active after Close")
	}
}

// TestCommandHostsStubInactive verifies that a stub-build host directory
// yields no dispatchable commands.
func TestCommandHostsStubInactive(t *testing.T) {
	t.Parallel()

	dir := testutils.TempDir(t, "gornx-plugins")
	ch := newCommandHosts(dir, time.Second, nil)
	if ch == nil {
		t.Fatal("newCommandHosts returned nil")
	}
	if ch.forCommand("echo") != nil {
		t.Fatal("forCommand returned a host in the stub build, want nil")
	}
	ch.close()
}

// TestErrPluginsNotLinked documents the stub-build error sentinel.
func TestErrPluginsNotLinked(t *testing.T) {
	t.Parallel()

	if !errors.Is(errPluginsNotLinked, errPluginsNotLinked) {
		t.Fatal("errPluginsNotLinked does not match itself")
	}
}
