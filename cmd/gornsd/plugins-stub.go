// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build !wago || (!linux && !darwin && !windows) || (!amd64 && !arm64)

// This file is the stub announce observer host, compiled whenever the wago
// runtime is not linked into the build (no -tags wago, or an unsupported
// platform). It keeps gornsd compiling with zero wasm overhead; announce
// handling keeps its existing behavior.

package main

import (
	"errors"
	"time"
)

// errAnnouncesNotLinked reports observer operations on a binary built
// without the wago runtime.
var errAnnouncesNotLinked = errors.New("announce observer support not compiled in (build with -tags wago)")

// ObserverHost is the inactive stub observer host.
type ObserverHost struct{}

// NewObserverHost returns an inactive observer host; the timeout and logger
// are part of the shared signature with the wago build and are unused here.
func NewObserverHost(_ time.Duration, _ func(format string, args ...any)) *ObserverHost {
	return &ObserverHost{}
}

// LoadPlugin always fails in the stub build.
func (h *ObserverHost) LoadPlugin(_ string) error { return errAnnouncesNotLinked }

// Active always reports false in the stub build.
func (h *ObserverHost) Active() bool { return false }

// HandleAnnounce always fails in the stub build.
func (h *ObserverHost) HandleAnnounce(_ []byte) error {
	return errAnnouncesNotLinked
}

// Close releases nothing in the stub build.
func (h *ObserverHost) Close() {}
