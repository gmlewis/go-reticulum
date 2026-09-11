// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)

// This file is the stub LXMF filter host, compiled whenever the wago runtime
// is not linked into the build (no -tags wago, or an unsupported platform).
// It keeps golxmd compiling with zero wasm overhead; the host stays inactive
// and every inbound message keeps its normal (unfiltered) delivery path.

package main

import (
	"errors"
	"time"
)

// errFiltersNotLinked reports filter operations on a binary built without
// the wago runtime.
var errFiltersNotLinked = errors.New("filter support not compiled in (build with -tags wago)")

// FilterHost is the inactive stub filter host.
type FilterHost struct{}

// NewFilterHost returns an inactive filter host; the wasm bytes are ignored.
// The timeout is part of the shared signature with the wago build and is
// unused here.
func NewFilterHost(_ time.Duration, _ func(format string, args ...any)) *FilterHost {
	return &FilterHost{}
}

// LoadPlugin always fails in the stub build.
func (h *FilterHost) LoadPlugin(_ string) error { return errFiltersNotLinked }

// Active always reports false in the stub build.
func (h *FilterHost) Active() bool { return false }

// HandleFilter always fails in the stub build.
func (h *FilterHost) HandleFilter(_ []byte) (bool, error) {
	return false, errFiltersNotLinked
}

// Close releases nothing in the stub build.
func (h *FilterHost) Close() {}
