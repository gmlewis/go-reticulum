//go:build integration && windows

// Copyright 2026 Glenn Lewis. All rights reserved.
//
// Use of this source code is governed by the Reticulum License
// that can be found in the LICENSE file.

package main

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {
	_ = cmd
}
