// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"os"
	"testing"
)

func tempTrustKeyHome(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "gornodeconf-trustkey-*")
	if err != nil {
		t.Fatalf("create temp home: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}
