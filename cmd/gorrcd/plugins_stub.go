// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)

// This file is the stub plugin host, compiled whenever the wago runtime is
// not linked into the build (no -tags wago, or an unsupported platform). It
// keeps gorrcd compiling with zero wasm overhead; the host stays inactive
// and every command falls through to the pre-plugin behavior.

package main

import (
	"errors"
	"time"
)

// errPluginsNotLinked reports plugin operations on a binary built without
// the wago runtime.
var errPluginsNotLinked = errors.New("plugin support not compiled in (build with -tags wago)")

// PluginHost is the inactive stub plugin host.
type PluginHost struct{}

// NewPluginHost returns an inactive plugin host; the wasm bytes are ignored.
// The timeout is part of the shared signature with the wago build and is
// unused here.
func NewPluginHost(_ time.Duration, _ func(format string, args ...any)) *PluginHost {
	return &PluginHost{}
}

// LoadPlugin always fails in the stub build.
func (h *PluginHost) LoadPlugin(_ string) error { return errPluginsNotLinked }

// Active always reports false in the stub build.
func (h *PluginHost) Active() bool { return false }

// HandleCommand always fails in the stub build.
func (h *PluginHost) HandleCommand(_ string) (string, error) {
	return "", errPluginsNotLinked
}

// Close releases nothing in the stub build.
func (h *PluginHost) Close() {}
