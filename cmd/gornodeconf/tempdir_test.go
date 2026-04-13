// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import (
	"path/filepath"
	"testing"

	"github.com/gmlewis/go-reticulum/testutils"
)

func tempSerialPort(t *testing.T) string {
	t.Helper()

	dir, cleanup := testutils.TempDir(t, "gornodeconf-port-*")
	t.Cleanup(cleanup)
	return filepath.Join(dir, "port")
}
