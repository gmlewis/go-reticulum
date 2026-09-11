// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)

// This file is the stub command plugin host, compiled whenever the wago
// runtime is not linked into the build (no -tags wago, or an unsupported
// platform). It keeps gornx compiling with zero wasm overhead; every
// requested command keeps its existing raw-exec path.

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

// NewPluginHost returns an inactive plugin host; the timeout and logger are
// part of the shared signature with the wago build and are unused here.
func NewPluginHost(_ time.Duration, _ func(format string, args ...any)) *PluginHost {
	return &PluginHost{}
}

// LoadPlugin always fails in the stub build.
func (h *PluginHost) LoadPlugin(_ string) error { return errPluginsNotLinked }

// Active always reports false in the stub build.
func (h *PluginHost) Active() bool { return false }

// HandleCommand always fails in the stub build.
func (h *PluginHost) HandleCommand(_ []byte, _ time.Duration) ([]byte, error) {
	return nil, errPluginsNotLinked
}

// Close releases nothing in the stub build.
func (h *PluginHost) Close() {}
