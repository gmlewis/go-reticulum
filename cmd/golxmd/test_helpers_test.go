// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "testing"

func skipShortIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration parity test in -short mode")
	}
}
