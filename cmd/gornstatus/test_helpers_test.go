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

func tempDirWithConfig(t *testing.T) (string, func()) {
	return testutils.TempDirWithConfig(t, "gornstatus-test-", func(dir string) string {
		instanceName := filepath.Base(dir)
		return "[reticulum]\nenable_transport = False\nshare_instance = Yes\ninstance_name = " + instanceName + "\n\n[logging]\nloglevel = 2\n"
	})
}
