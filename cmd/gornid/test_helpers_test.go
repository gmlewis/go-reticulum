// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func skipShortIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration parity test in -short mode")
	}
}

func tempDir(t *testing.T) (string, func()) {
	return testutils.TempDir(t, "gornid-test-")
}
