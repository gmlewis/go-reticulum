// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

//go:build integration

package main

// Slow integration-only tests live here: they exercise real-time protocol
// timing that needs multi-second waits even with injected durations.

import (
	"testing"
)

func TestSlowIntegrationPlaceholder(t *testing.T) {
	t.Parallel()
	t.Skip("placeholder - no slow integration tests currently in this file")
}
